// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package awss3

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/goccy/go-json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/gofrs/uuid"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elastic/beats/v7/libbeat/logp"
)

const testTimeout = 10 * time.Second

var errFakeConnectivityFailure = errors.New("fake connectivity failure")

func TestSQSReceiver(t *testing.T) {
	logp.TestingSetup()
	const maxMessages = 5

	t.Run("ReceiveMessage success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		ctrl, ctx := gomock.WithContext(ctx, t)
		defer ctrl.Finish()
		mockAPI := NewMockSQSAPI(ctrl)
		mockMsgHandler := NewMockSQSProcessor(ctrl)
		msg := newSQSMessage(newS3Event("log.json"))

		gomock.InOrder(
			// Initial ReceiveMessage for maxMessages.
			mockAPI.EXPECT().
				ReceiveMessage(gomock.Any(), gomock.Eq(maxMessages)).
				Times(1).
				DoAndReturn(func(_ context.Context, _ int) ([]sqs.Message, error) {
					// Return single message.
					return []sqs.Message{msg}, nil
				}),

			// Follow up ReceiveMessages for either maxMessages-1 or maxMessages
			// depending on how long processing of previous message takes.
			mockAPI.EXPECT().
				ReceiveMessage(gomock.Any(), gomock.Any()).
				Times(1).
				DoAndReturn(func(_ context.Context, _ int) ([]sqs.Message, error) {
					// Stop the test.
					cancel()
					return nil, nil
				}),
		)

		// Expect the one message returned to have been processed.
		mockMsgHandler.EXPECT().
			ProcessSQS(gomock.Any(), gomock.Eq(&msg)).
			Times(1).
			Return(nil)

		// Execute sqsReader and verify calls/state.
		receiver := newSQSReader(logp.NewLogger(inputName), nil, mockAPI, maxMessages, mockMsgHandler)
		require.NoError(t, receiver.Receive(ctx))
		assert.Equal(t, maxMessages, receiver.workerSem.Available())
	})

	t.Run("retry after ReceiveMessage error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), sqsRetryDelay+testTimeout)
		defer cancel()

		ctrl, ctx := gomock.WithContext(ctx, t)
		defer ctrl.Finish()
		mockAPI := NewMockSQSAPI(ctrl)
		mockMsgHandler := NewMockSQSProcessor(ctrl)

		gomock.InOrder(
			// Initial ReceiveMessage gets an error.
			mockAPI.EXPECT().
				ReceiveMessage(gomock.Any(), gomock.Eq(maxMessages)).
				Times(1).
				DoAndReturn(func(_ context.Context, _ int) ([]sqs.Message, error) {
					return nil, errFakeConnectivityFailure
				}),
			// After waiting for sqsRetryDelay, it retries.
			mockAPI.EXPECT().
				ReceiveMessage(gomock.Any(), gomock.Eq(maxMessages)).
				Times(1).
				DoAndReturn(func(_ context.Context, _ int) ([]sqs.Message, error) {
					cancel()
					return nil, nil
				}),
		)

		// Execute SQSReceiver and verify calls/state.
		receiver := newSQSReader(logp.NewLogger(inputName), nil, mockAPI, maxMessages, mockMsgHandler)
		require.NoError(t, receiver.Receive(ctx))
		assert.Equal(t, maxMessages, receiver.workerSem.Available())
	})
}

func newSQSMessage(events ...s3EventV2) sqs.Message {
	body, err := json.Marshal(s3EventsV2{Records: events})
	if err != nil {
		panic(err)
	}

	hash := sha256.Sum256(body)
	id, _ := uuid.FromBytes(hash[:16])
	messageID := id.String()
	receipt := "receipt-" + messageID
	bodyStr := string(body)

	return sqs.Message{
		Body:          &bodyStr,
		MessageId:     &messageID,
		ReceiptHandle: &receipt,
	}
}

func newSNSSQSMessage() sqs.Message {
	body, err := json.Marshal(s3EventsV2{
		TopicArn: "arn:aws:sns:us-east-1:1234:sns-topic",
		Message:  "{\"Records\":[{\"eventSource\":\"aws:s3\",\"awsRegion\":\"us-east-1\",\"eventName\":\"ObjectCreated:Put\",\"s3\":{\"configurationId\":\"sns-notification-vpc-flow-logs\",\"bucket\":{\"name\":\"vpc-flow-logs-ks\",\"arn\":\"arn:aws:s3:::vpc-flow-logs-ks\"},\"object\":{\"key\":\"test-object-key\"}}}]}",
	})
	if err != nil {
		panic(err)
	}

	hash := sha256.Sum256(body)
	id, _ := uuid.FromBytes(hash[:16])
	messageID := id.String()
	receipt := "receipt-" + messageID
	bodyStr := string(body)

	return sqs.Message{
		Body:          &bodyStr,
		MessageId:     &messageID,
		ReceiptHandle: &receipt,
	}
}

func newS3Event(key string) s3EventV2 {
	record := s3EventV2{
		AWSRegion:   "us-east-1",
		EventSource: "aws:s3",
		EventName:   "ObjectCreated:Put",
	}
	record.S3.Bucket.Name = "foo"
	record.S3.Bucket.ARN = "arn:aws:s3:::foo"
	record.S3.Object.Key = key
	return record
}
