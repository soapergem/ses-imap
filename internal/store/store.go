// Package store provides the DynamoDB + S3 storage layer for IMAP message metadata and bodies.
package store

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/soapergem/ses-imap/internal/config"
)

// MessageMeta represents message metadata stored in DynamoDB.
type MessageMeta struct {
	Mailbox     string   `dynamodbav:"mailbox"`
	UID         uint32   `dynamodbav:"uid"`
	S3Key       string   `dynamodbav:"s3_key"`
	MessageID   string   `dynamodbav:"message_id"`
	FromAddr    string   `dynamodbav:"from_addr"`
	FromDisplay string   `dynamodbav:"from_display"`
	ToAddr      string   `dynamodbav:"to_addr"`
	Subject     string   `dynamodbav:"subject"`
	Date        string   `dynamodbav:"internal_date"`
	Size        uint32   `dynamodbav:"size"`
	Flags       []string `dynamodbav:"flags,stringset,omitemptyelem"`
}

// MailboxMeta represents per-mailbox metadata stored in DynamoDB.
type MailboxMeta struct {
	Mailbox     string `dynamodbav:"mailbox"`
	UID         uint32 `dynamodbav:"uid"` // Always 0 for metadata sentinel.
	UIDNext     uint32 `dynamodbav:"uid_next"`
	UIDValidity uint32 `dynamodbav:"uid_validity"`
}

// Store provides access to message metadata in DynamoDB and message bodies in S3.
type Store struct {
	dynamo    *dynamodb.Client
	s3client  *s3.Client
	tableName string
	bucket    string
}

// New creates a new Store.
func New(ctx context.Context, cfg *config.Config) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &Store{
		dynamo:    dynamodb.NewFromConfig(awsCfg),
		s3client:  s3.NewFromConfig(awsCfg),
		tableName: cfg.DynamoDBTable,
		bucket:    cfg.S3Bucket,
	}, nil
}

// EnsureMailbox ensures a mailbox metadata record exists, creating it if necessary.
// Returns the current mailbox metadata.
func (s *Store) EnsureMailbox(ctx context.Context, mailbox string) (*MailboxMeta, error) {
	meta := &MailboxMeta{
		Mailbox:     mailbox,
		UID:         0,
		UIDNext:     1,
		UIDValidity: uint32(time.Now().Unix()),
	}

	item, err := attributevalue.MarshalMap(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling mailbox metadata: %w", err)
	}

	// Only create if it doesn't exist.
	cond := expression.AttributeNotExists(expression.Name("mailbox"))
	expr, err := expression.NewBuilder().WithCondition(cond).Build()
	if err != nil {
		return nil, fmt.Errorf("building condition expression: %w", err)
	}

	_, err = s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                 &s.tableName,
		Item:                      item,
		ConditionExpression:       expr.Condition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	if err != nil {
		// Condition check failed means it already exists -- that's fine.
		if !isConditionalCheckFailed(err) {
			return nil, fmt.Errorf("creating mailbox metadata: %w", err)
		}
	}

	// Read back the current state.
	return s.GetMailboxMeta(ctx, mailbox)
}

// GetMailboxMeta retrieves mailbox metadata.
func (s *Store) GetMailboxMeta(ctx context.Context, mailbox string) (*MailboxMeta, error) {
	key, err := attributevalue.MarshalMap(map[string]interface{}{
		"mailbox": mailbox,
		"uid":     0,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling key: %w", err)
	}

	result, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("getting mailbox metadata: %w", err)
	}
	if result.Item == nil {
		return nil, fmt.Errorf("mailbox %q not found", mailbox)
	}

	meta := &MailboxMeta{}
	if err := attributevalue.UnmarshalMap(result.Item, meta); err != nil {
		return nil, fmt.Errorf("unmarshaling mailbox metadata: %w", err)
	}
	return meta, nil
}

// AllocateUID atomically increments UIDNext and returns the allocated UID.
func (s *Store) AllocateUID(ctx context.Context, mailbox string) (uint32, error) {
	key, err := attributevalue.MarshalMap(map[string]interface{}{
		"mailbox": mailbox,
		"uid":     0,
	})
	if err != nil {
		return 0, fmt.Errorf("marshaling key: %w", err)
	}

	result, err := s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        &s.tableName,
		Key:              key,
		UpdateExpression: aws.String("SET uid_next = uid_next + :one"),
		ExpressionAttributeValues: map[string]dynamotypes.AttributeValue{
			":one": &dynamotypes.AttributeValueMemberN{Value: "1"},
		},
		ReturnValues: dynamotypes.ReturnValueUpdatedOld,
	})
	if err != nil {
		return 0, fmt.Errorf("allocating UID: %w", err)
	}

	// The old value of uid_next is the UID we allocated.
	var oldNext float64
	if v, ok := result.Attributes["uid_next"]; ok {
		if err := attributevalue.Unmarshal(v, &oldNext); err != nil {
			return 0, fmt.Errorf("unmarshaling uid_next: %w", err)
		}
	}

	return uint32(oldNext), nil
}

// MessageExistsByS3Key checks if a message with the given S3 key already exists in a mailbox.
func (s *Store) MessageExistsByS3Key(ctx context.Context, mailbox, s3Key string) (bool, error) {
	keyCond := expression.KeyAnd(
		expression.Key("mailbox").Equal(expression.Value(mailbox)),
		expression.Key("uid").GreaterThan(expression.Value(0)),
	)
	filter := expression.Name("s3_key").Equal(expression.Value(s3Key))
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).WithFilter(filter).Build()
	if err != nil {
		return false, fmt.Errorf("building expression: %w", err)
	}

	result, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &s.tableName,
		KeyConditionExpression:    expr.KeyCondition(),
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		Limit:                     aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("querying for existing message: %w", err)
	}

	return len(result.Items) > 0, nil
}

// PutMessage writes a message metadata record to DynamoDB.
func (s *Store) PutMessage(ctx context.Context, msg *MessageMeta) error {
	item, err := attributevalue.MarshalMap(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	_, err = s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("putting message: %w", err)
	}
	return nil
}

// ListMessages returns all message metadata for a mailbox, ordered by UID.
func (s *Store) ListMessages(ctx context.Context, mailbox string) ([]*MessageMeta, error) {
	keyCond := expression.KeyAnd(
		expression.Key("mailbox").Equal(expression.Value(mailbox)),
		expression.Key("uid").GreaterThan(expression.Value(0)),
	)
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return nil, fmt.Errorf("building expression: %w", err)
	}

	result, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &s.tableName,
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}

	var messages []*MessageMeta
	for _, item := range result.Items {
		msg := &MessageMeta{}
		if err := attributevalue.UnmarshalMap(item, msg); err != nil {
			log.Printf("warning: skipping malformed message item: %v", err)
			continue
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// ListMessagesUIDRange returns messages in a UID range (inclusive).
func (s *Store) ListMessagesUIDRange(ctx context.Context, mailbox string, uidMin, uidMax uint32) ([]*MessageMeta, error) {
	keyCond := expression.KeyAnd(
		expression.Key("mailbox").Equal(expression.Value(mailbox)),
		expression.Key("uid").Between(expression.Value(uidMin), expression.Value(uidMax)),
	)
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return nil, fmt.Errorf("building expression: %w", err)
	}

	result, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &s.tableName,
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}

	var messages []*MessageMeta
	for _, item := range result.Items {
		msg := &MessageMeta{}
		if err := attributevalue.UnmarshalMap(item, msg); err != nil {
			log.Printf("warning: skipping malformed message item: %v", err)
			continue
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetMessage retrieves a single message by mailbox and UID.
func (s *Store) GetMessage(ctx context.Context, mailbox string, uid uint32) (*MessageMeta, error) {
	key, err := attributevalue.MarshalMap(map[string]interface{}{
		"mailbox": mailbox,
		"uid":     uid,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling key: %w", err)
	}

	result, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("getting message: %w", err)
	}
	if result.Item == nil {
		return nil, nil
	}

	msg := &MessageMeta{}
	if err := attributevalue.UnmarshalMap(result.Item, msg); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %w", err)
	}
	return msg, nil
}

// UpdateFlags replaces the flags on a message.
func (s *Store) UpdateFlags(ctx context.Context, mailbox string, uid uint32, flags []string) error {
	key, err := attributevalue.MarshalMap(map[string]interface{}{
		"mailbox": mailbox,
		"uid":     uid,
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	if len(flags) == 0 {
		// DynamoDB doesn't allow empty string sets, so remove the attribute.
		_, err = s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        &s.tableName,
			Key:              key,
			UpdateExpression: aws.String("REMOVE flags"),
		})
	} else {
		_, err = s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        &s.tableName,
			Key:              key,
			UpdateExpression: aws.String("SET flags = :f"),
			ExpressionAttributeValues: map[string]dynamotypes.AttributeValue{
				":f": &dynamotypes.AttributeValueMemberSS{Value: flags},
			},
		})
	}
	if err != nil {
		return fmt.Errorf("updating flags: %w", err)
	}
	return nil
}

// DeleteMessage removes a message metadata record.
func (s *Store) DeleteMessage(ctx context.Context, mailbox string, uid uint32) error {
	key, err := attributevalue.MarshalMap(map[string]interface{}{
		"mailbox": mailbox,
		"uid":     uid,
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	_, err = s.dynamo.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tableName,
		Key:       key,
	})
	if err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}
	return nil
}

// FetchMessageBody retrieves the raw RFC 5322 message body from S3.
func (s *Store) FetchMessageBody(ctx context.Context, s3Key string) ([]byte, error) {
	result, err := s.s3client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &s3Key,
	})
	if err != nil {
		return nil, fmt.Errorf("getting S3 object %q: %w", s3Key, err)
	}
	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("reading S3 object body: %w", err)
	}
	return body, nil
}

// GetMessageSize returns the size of the raw message in S3.
func (s *Store) GetMessageSize(ctx context.Context, s3Key string) (uint32, error) {
	result, err := s.s3client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &s3Key,
	})
	if err != nil {
		return 0, fmt.Errorf("heading S3 object %q: %w", s3Key, err)
	}
	if result.ContentLength != nil {
		return uint32(*result.ContentLength), nil
	}
	return 0, nil
}

// isConditionalCheckFailed checks if a DynamoDB error is a conditional check failure.
func isConditionalCheckFailed(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "ConditionalCheckFailedException")
}

// HasFlag returns true if the message has the given flag.
func HasFlag(msg *MessageMeta, flag string) bool {
	for _, f := range msg.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// FormatUID formats a UID as a string for DynamoDB sort key.
func FormatUID(uid uint32) string {
	return strconv.FormatUint(uint64(uid), 10)
}
