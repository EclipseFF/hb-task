package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hb-task/internal/core"
	"hb-task/internal/integration/camunda"
	"hb-task/internal/integration/postgres"

	"github.com/segmentio/kafka-go"
)

const Topic = "vacation.decisions"

func NewWriter(brokers string) *kafka.Writer {
	return &kafka.Writer{Addr: kafka.TCP(strings.Split(brokers, ",")...), Topic: Topic,
		Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, WriteTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second, MaxAttempts: 3, BatchTimeout: 10 * time.Millisecond}
}

func Dispatch(ctx context.Context, store *postgres.Store, cam *camunda.Client, writer *kafka.Writer) {
	for ctx.Err() == nil {
		opCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		found, err := store.DispatchOne(opCtx, func(ctx context.Context, m postgres.OutboxMessage) (string, error) {
			if m.Kind == "start_process" {
				r, err := store.Request(ctx, m.RequestID)
				if err != nil {
					return "", err
				}
				return cam.Start(ctx, r)
			}
			return "", writer.WriteMessages(ctx, kafka.Message{Key: []byte(m.RequestID), Value: m.Payload})
		})

		cancel()

		if err != nil {
			slog.Error("outbox delivery failed", "error", err)
		}
		if !found || err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

type Consumer struct {
	Brokers, ClickHouseURL, Username, Password string
}

func (c Consumer) Run(ctx context.Context) error {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: strings.Split(c.Brokers, ","), Topic: Topic, GroupID: "vacation-clickhouse-v1",
		MinBytes: 1, MaxBytes: 1 << 20, CommitInterval: 0, StartOffset: kafka.FirstOffset,
	})

	defer reader.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	for ctx.Err() == nil {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("fetch Kafka event", "error", err)
			if !pause(ctx) {
				return nil
			}
			continue
		}

		for ctx.Err() == nil {
			err = c.insert(ctx, client, message.Value)
			if err == nil {
				err = reader.CommitMessages(ctx, message)
			}
			if err == nil {
				break
			}
			slog.Error("analytics delivery failed", "partition", message.Partition, "offset", message.Offset, "error", err)
			if !pause(ctx) {
				return nil
			}
		}
	}
	return nil
}

func pause(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(3 * time.Second):
		return true
	}
}

func (c Consumer) insert(ctx context.Context, client *http.Client, payload []byte) error {
	var event core.DecisionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	if event.Type != "vacation.decided" || event.Version != 1 || !event.Result.Final() || event.EventID == "" || event.OccurredAt.IsZero() {
		return fmt.Errorf("unsupported or invalid event")
	}

	query := "INSERT INTO vacation.decisions FORMAT JSONEachRow"
	u := c.ClickHouseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("clickhouse status %d: %s", resp.StatusCode, body)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
