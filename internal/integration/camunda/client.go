package camunda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"hb-task/internal/core"
	"hb-task/workflow"
)

type Client struct {
	URL, Username, Password string
	HTTP                    *http.Client
}

func New(baseURL, username, password string) *Client {
	return &Client{baseURL, username, password, &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) request(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("camunda %s %s: status %d: %s", method, path, resp.StatusCode, data)
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func (c *Client) json(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	return c.request(ctx, method, path, "application/json", body, out)
}

func (c *Client) Deploy(ctx context.Context) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, name := range []string{"vacation.bpmn", "vacation-result.dmn", "manager.form"} {
		data, err := workflow.Files.ReadFile(name)
		if err != nil {
			return err
		}
		part, err := w.CreateFormFile("resources", name)
		if err != nil {
			return err
		}
		if _, err = part.Write(data); err != nil {
			return err
		}
	}

	if err := w.Close(); err != nil {
		return err
	}
	return c.request(ctx, http.MethodPost, "/deployments", w.FormDataContentType(), &buf, nil)
}

type Task struct {
	ID         string         `json:"jobKey"`
	Topic      string         `json:"type"`
	Retries    int            `json:"retries"`
	LeaseToken string         `json:"jobLeaseToken"`
	Variables  map[string]any `json:"variables"`
}

func (t Task) String(name string) (string, error) {
	v, ok := t.Variables[name].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("missing string variable %s", name)
	}
	return v, nil
}

func (c *Client) Start(ctx context.Context, r core.Request) (string, error) {
	var existing struct {
		Items []struct {
			Key string `json:"processInstanceKey"`
		} `json:"items"`
	}

	if err := c.json(ctx, http.MethodPost, "/process-instances/search", map[string]any{
		"filter": map[string]any{"processDefinitionId": "vacation", "businessId": r.ID},
	}, &existing); err != nil {
		return "", err
	}

	if len(existing.Items) > 0 {
		return existing.Items[0].Key, nil
	}
	in := map[string]any{
		"processDefinitionId": "vacation", "businessId": r.ID,
		"variables": map[string]any{
			"requestId": r.ID, "employeeId": r.EmployeeID,
			"startDate": r.StartDate, "endDate": r.EndDate, "comment": r.Comment,
			"days": r.Days, "managerApproved": false,
		},
	}

	var result struct {
		Key string `json:"processInstanceKey"`
	}
	err := c.json(ctx, http.MethodPost, "/process-instances", in, &result)
	return result.Key, err
}

func (c *Client) Fetch(ctx context.Context, workerID, topic string) ([]Task, error) {
	var result struct {
		Jobs []Task `json:"jobs"`
	}

	err := c.json(ctx, http.MethodPost, "/jobs/activation", map[string]any{
		"worker": workerID, "type": topic, "maxJobsToActivate": 1,
		"timeout": 60000, "requestTimeout": 20000, "withLease": true,
	}, &result)
	return result.Jobs, err
}

func (c *Client) Complete(ctx context.Context, task Task, vars map[string]any) error {
	return c.json(ctx, http.MethodPost, "/jobs/"+url.PathEscape(task.ID)+"/completion",
		map[string]any{"variables": vars, "jobLeaseToken": task.LeaseToken}, nil)
}

func (c *Client) Failure(ctx context.Context, task Task, cause error) error {
	return c.json(ctx, http.MethodPost, "/jobs/"+url.PathEscape(task.ID)+"/failure",
		map[string]any{"errorMessage": cause.Error(), "retries": max(0, task.Retries-1),
			"retryBackOff": 10000, "jobLeaseToken": task.LeaseToken}, nil)
}
