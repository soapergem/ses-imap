// Command ses-lambda is an AWS Lambda function triggered by SES receipt rules.
// It parses incoming email headers and writes metadata to DynamoDB.
package main

import (
	"context"
	"fmt"
	"log"
	"mime"
	"net/mail"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/soapergem/ses-imap/internal/config"
	"github.com/soapergem/ses-imap/internal/store"
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event events.SimpleEmailEvent) error {
	cfg, err := config.LambdaFromEnv()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	st, err := store.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("creating store: %w", err)
	}

	for _, record := range event.Records {
		if err := processRecord(ctx, cfg, st, record); err != nil {
			log.Printf("error processing record: %v", err)
			return err
		}
	}

	return nil
}

func processRecord(ctx context.Context, cfg *config.Config, st *store.Store, record events.SimpleEmailRecord) error {
	sesMsg := record.SES

	// Determine the recipient mailbox.
	mailbox := cfg.DefaultMailbox
	if len(sesMsg.Receipt.Recipients) > 0 {
		mailbox = strings.ToLower(sesMsg.Receipt.Recipients[0])
	}

	// Derive the S3 key from the recipient address.
	// SES stores objects as: <domain>/<localpart>/<message-id>
	// e.g., gordon@gemovationlabs.com -> gemovationlabs.com/gordon/<message-id>
	s3Key := recipientToS3Key(mailbox, sesMsg.Mail.MessageID)
	if cfg.S3Prefix != "" {
		s3Key = cfg.S3Prefix + sesMsg.Mail.MessageID
	}
	log.Printf("processing message %s (S3 key: %s)", sesMsg.Mail.MessageID, s3Key)

	// Fetch the raw message from S3 to parse headers.
	body, err := st.FetchMessageBody(ctx, s3Key)
	if err != nil {
		return fmt.Errorf("fetching message body: %w", err)
	}

	// Parse the RFC 5322 message headers.
	msg, err := mail.ReadMessage(strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("parsing message: %w", err)
	}

	// Ensure the mailbox exists.
	if _, err := st.EnsureMailbox(ctx, mailbox); err != nil {
		return fmt.Errorf("ensuring mailbox %q: %w", mailbox, err)
	}

	// Allocate a UID.
	uid, err := st.AllocateUID(ctx, mailbox)
	if err != nil {
		return fmt.Errorf("allocating UID: %w", err)
	}

	// Extract header fields.
	fromAddr, fromDisplay := parseAddress(msg.Header.Get("From"))
	toAddr, _ := parseAddress(msg.Header.Get("To"))
	subject := decodeHeader(msg.Header.Get("Subject"))
	messageID := msg.Header.Get("Message-ID")
	date := msg.Header.Get("Date")

	meta := &store.MessageMeta{
		Mailbox:     mailbox,
		UID:         uid,
		S3Key:       s3Key,
		MessageID:   messageID,
		FromAddr:    fromAddr,
		FromDisplay: fromDisplay,
		ToAddr:      toAddr,
		Subject:     subject,
		Date:        date,
		Size:        uint32(len(body)),
		Flags:       []string{},
	}

	if err := st.PutMessage(ctx, meta); err != nil {
		return fmt.Errorf("writing message metadata: %w", err)
	}

	log.Printf("indexed message UID=%d mailbox=%s subject=%q", uid, mailbox, subject)
	return nil
}

// parseAddress extracts the email address and display name from an RFC 5322 address.
func parseAddress(raw string) (addr, display string) {
	if raw == "" {
		return "", ""
	}

	parsed, err := mail.ParseAddress(raw)
	if err != nil {
		// Fall back to using the raw string as the address.
		return raw, ""
	}
	return parsed.Address, parsed.Name
}

// recipientToS3Key derives the S3 object key from a recipient email address
// and SES message ID. SES stores objects as <domain>/<localpart>/<message-id>.
func recipientToS3Key(recipient, messageID string) string {
	parts := strings.SplitN(recipient, "@", 2)
	if len(parts) != 2 {
		// Fallback: use the recipient as-is (e.g., a catch-all domain).
		return recipient + "/" + messageID
	}
	localpart := parts[0]
	domain := parts[1]
	return domain + "/" + localpart + "/" + messageID
}

// decodeHeader decodes RFC 2047 encoded header values.
func decodeHeader(raw string) string {
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(raw)
	if err != nil {
		return raw
	}
	return decoded
}
