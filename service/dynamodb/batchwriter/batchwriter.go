package batchwriter

import (
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/dynamodbiface"
)

const defaultFlushAmount = 25
const defaultRequestBufferCap = 50

// BatchWriter wraps a dynamodb client to expose a simple PutItem/DeleteItem
// API that buffers requests and takes advantage of BatchWriteItem behind
// the scenes.
type BatchWriter struct {
	// Size of the buffer in which it will be flushed.
	// Do not set to a number above 25 as DynamoDB rejects BatchWrites with
	// more than 25 items.
	FlushAmount   int
	tableName     string
	client        dynamodbiface.DynamoDBAPI
	primaryKeys   []string
	requestBuffer []dynamodb.WriteRequest
}

// New creates a new BatchWriter that will write to table `tableName`
// using `client`.
//
// New will return an error if it fails to access the table information with a
// DescribeTableRequest.
func New(tableName string, client dynamodbiface.DynamoDBAPI) (*BatchWriter, error) {
	describeTableReq := client.DescribeTableRequest(&dynamodb.DescribeTableInput{
		TableName: &tableName,
	})
	describeTableOut, err := describeTableReq.Send()
	if err != nil {
		return &BatchWriter{}, err
	}
	// List of primary keys of the table. We will get them from a
	// DescribeTable request to DynamoDB.
	pKeys := []string{}
	for _, key := range describeTableOut.Table.KeySchema {
		pKeys = append(pKeys, *key.AttributeName)
	}
	batchWriter := NewWithPrimaryKeys(tableName, client, pKeys)
	return batchWriter, nil
}

// NewWithPrimaryKeys creates a new BatchWriter using `primaryKeys` as the
// specified primary keys instead of getting them from a call to
// DescribeTable.
func NewWithPrimaryKeys(tableName string, client dynamodbiface.DynamoDBAPI,
	primaryKeys []string) *BatchWriter {

	requestBuffer := make(
		[]dynamodb.WriteRequest, 0, defaultRequestBufferCap,
	)
	batchWriter := &BatchWriter{
		FlushAmount:   defaultFlushAmount,
		tableName:     tableName,
		client:        client,
		primaryKeys:   primaryKeys,
		requestBuffer: requestBuffer,
	}
	return batchWriter
}

// WrapPutItem wraps a PutRequest to use with BatchWriter.Add.
func WrapPutItem(putRequest *dynamodb.PutRequest) dynamodb.WriteRequest {
	writeRequest := dynamodb.WriteRequest{PutRequest: putRequest}
	return writeRequest
}

// WrapDeleteItem wraps a DeleteRequest to use with BatchWriter.Add.
func WrapDeleteItem(deleteRequest *dynamodb.DeleteRequest) dynamodb.WriteRequest {
	writeRequest := dynamodb.WriteRequest{DeleteRequest: deleteRequest}
	return writeRequest
}

func (b *BatchWriter) flushIfNeeded() error {
	if len(b.requestBuffer) < b.FlushAmount {
		return nil
	}
	err := b.Flush()
	return err
}

// Add is used to queue a PutItem or DeleteItem operation.
//
// Most normally, it will be used in conjunction with WrapPutItem and
// WrapDeleteItem.
func (b *BatchWriter) Add(writeRequest dynamodb.WriteRequest) error {
	b.requestBuffer = append(b.requestBuffer, writeRequest)
	err := b.flushIfNeeded()
	return err
}

// Flush makes a BatchWriteItem with some or all requests that have been added
// so far to the buffer by means of PutItem and DeleteIem.
//
// Any unprocessed items sent in the response will be added to the end of the
// buffer, to be sent at a later date.
func (b *BatchWriter) Flush() error {
	if b.Empty() {
		return nil
	}
	flushBound := b.FlushAmount
	if flushBound > len(b.requestBuffer) {
		flushBound = len(b.requestBuffer)
	}
	itemsToSend := b.requestBuffer[:flushBound]
	b.requestBuffer = b.requestBuffer[flushBound:]
	output, err := b.sendRequestItems(itemsToSend)
	if err != nil {
		return err
	}
	// Check for unprocessed items and, if there are any, add them to the
	// back of the buffer.
	unpItems, ok := output.UnprocessedItems[b.tableName]
	if ok {
		b.requestBuffer = append(b.requestBuffer, unpItems...)
	}
	return nil
}

func (b *BatchWriter) sendRequestItems(
	requestItems []dynamodb.WriteRequest,
) (
	*dynamodb.BatchWriteItemOutput, error,
) {
	mappedItems := map[string][]dynamodb.WriteRequest{
		b.tableName: requestItems,
	}
	batchInput := dynamodb.BatchWriteItemInput{RequestItems: mappedItems}
	batchRequest := b.client.BatchWriteItemRequest(&batchInput)
	output, err := batchRequest.Send()
	return output, err
}

// Empty returns whether or not the request buffer is empty.
func (b *BatchWriter) Empty() bool {
	return len(b.requestBuffer) == 0
}
