package camunda

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"hb-task/internal/core"
)

func TestStartReusesCompletedProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/process-instances/search" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", 500)
			return
		}
		fmt.Fprint(w, `{"items":[{"processInstanceKey":"2251799813685250","state":"COMPLETED"}]}`)
	}))
	t.Cleanup(server.Close)
	key, err := New(server.URL, "manager", "manager").Start(t.Context(), core.Request{ID: "request-id"})
	if err != nil || key != "2251799813685250" {
		t.Fatalf("Start() = %q, %v", key, err)
	}
}

func TestJobActivationAndCompletionPreserveLeaseAndBoolean(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "manager" || password != "manager" {
			t.Error("missing basic auth")
		}
		switch r.URL.Path {
		case "/jobs/activation":
			if body["type"] != "check-balance" || body["withLease"] != true {
				t.Errorf("activation: %v", body)
			}
			fmt.Fprint(w, `{"jobs":[{"jobKey":"2251799813685251","type":"check-balance","retries":3,"jobLeaseToken":"lease-token","variables":{"requestId":"request-id"}}]}`)
		case "/jobs/2251799813685251/completion":
			vars, ok := body["variables"].(map[string]any)
			if !ok || vars["balanceSufficient"] != false || body["jobLeaseToken"] != "lease-token" {
				t.Errorf("completion: %v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, "manager", "manager")
	jobs, err := client.Fetch(t.Context(), "worker", "check-balance")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Fetch() = %v, %v", jobs, err)
	}
	if id, err := jobs[0].String("requestId"); err != nil || id != "request-id" {
		t.Fatalf("requestId = %q, %v", id, err)
	}
	if err := client.Complete(t.Context(), jobs[0], map[string]any{"balanceSufficient": false}); err != nil {
		t.Fatal(err)
	}
}
