package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestOllamaSendsTheConfiguredContext covers the settings the interface
// shows: the context window and the call timeout have to reach the request,
// or the page is telling the user something that is not so.
func TestOllamaSendsTheConfiguredContext(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"response":"{\"module\":\"x\"}"}`))
	}))
	defer server.Close()

	client := New(Config{Protocol: ProtocolOllama, Endpoint: server.URL, Model: "qwen3:latest"})
	client.Configure(4096, 30)
	answer, err := client.Generate(context.Background(), "system", "prompt", true, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	if answer != `{"module":"x"}` {
		t.Errorf("the answer reads %q", answer)
	}
	options, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("the request carries no options: %v", body)
	}
	if got := int(options["num_ctx"].(float64)); got != 4096 {
		t.Errorf("num_ctx sent is %d, expected the configured 4096", got)
	}
	if got := int(options["num_predict"].(float64)); got != DefaultMaxTokens {
		t.Errorf("num_predict sent is %d, expected the default %d", got, DefaultMaxTokens)
	}
	if body["format"] != "json" || body["system"] != "system" {
		t.Errorf("the request reads %v", body)
	}
	if client.HTTP.Timeout != 30*time.Second {
		t.Errorf("timeout is %v, expected the configured 30s", client.HTTP.Timeout)
	}

	// A client left alone sends the defaults.
	plain := New(Config{Protocol: ProtocolOllama, Endpoint: server.URL, Model: "qwen3:latest"})
	plain.Configure(0, 0)
	if _, err := plain.Generate(context.Background(), "", "p", false, 0.1); err != nil {
		t.Fatal(err)
	}
	if got := int(body["options"].(map[string]any)["num_ctx"].(float64)); got != DefaultNumCtx {
		t.Errorf("num_ctx sent is %d, expected the default %d", got, DefaultNumCtx)
	}

	// The budget is the run's own where it sets one: a long document's answer
	// is cut at the protocol's default, which loses the stage.
	roomy := New(Config{Protocol: ProtocolOllama, Endpoint: server.URL, Model: "qwen3:latest", MaxTokens: 9000})
	if _, err := roomy.Generate(context.Background(), "", "p", false, 0.1); err != nil {
		t.Fatal(err)
	}
	if got := int(body["options"].(map[string]any)["num_predict"].(float64)); got != 9000 {
		t.Errorf("num_predict sent is %d, expected the run's 9000", got)
	}
}

// TestOpenAIChatCompletions covers the OpenAI-compatible protocol: the key
// as a bearer token, the system message as the first turn, a JSON answer
// asked for with response_format, and the answer read from the first
// choice.
func TestOpenAIChatCompletions(t *testing.T) {
	var (
		path, auth string
		body       map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"function_points\":[]}"}}]}`))
	}))
	defer server.Close()

	// An endpoint without a path of its own takes the /v1 prefix the API
	// hangs its paths off.
	client := New(Config{Protocol: ProtocolOpenAI, Endpoint: server.URL, APIKey: "sk-test", Model: "deepseek-chat"})
	answer, err := client.Generate(context.Background(), "system", "prompt", true, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("the request went to %s", path)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("the request carries %q", auth)
	}
	if body["model"] != "deepseek-chat" || body["temperature"].(float64) != 0.3 {
		t.Errorf("the request reads %v", body)
	}
	if _, ok := body["response_format"].(map[string]any); !ok {
		t.Errorf("a JSON answer is not asked for: %v", body)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("the request carries %d messages", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "system" {
		t.Errorf("the system message reads %v", first)
	}
	if answer != `{"function_points":[]}` {
		t.Errorf("the answer reads %q", answer)
	}
}

// TestOpenAIRetriesWithoutTheFieldsAProviderRefuses covers a provider that
// refuses a field the protocol allows, as OpenAI's reasoning models refuse
// temperature and max_tokens: the call goes again without them, since the
// prompt asks for JSON on its own.
func TestOpenAIRetriesWithoutTheFieldsAProviderRefuses(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'temperature' is not supported with this model."}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client := New(Config{Protocol: ProtocolOpenAI, Endpoint: server.URL + "/v1", Model: "o3-mini"})
	answer, err := client.Generate(context.Background(), "", "prompt", true, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "ok" {
		t.Errorf("the answer reads %q", answer)
	}
	if len(bodies) != 2 {
		t.Fatalf("the endpoint was asked %d times", len(bodies))
	}
	for _, field := range []string{"temperature", "max_tokens", "response_format"} {
		if _, ok := bodies[1][field]; ok {
			t.Errorf("the second request still carries %s: %v", field, bodies[1])
		}
	}
	if bodies[1]["model"] != "o3-mini" {
		t.Errorf("the second request reads %v", bodies[1])
	}
}

// TestAnthropicMessages covers the Messages API: the key as x-api-key, the
// system prompt as a field of its own, and the answer read from its content
// blocks.
func TestAnthropicMessages(t *testing.T) {
	var (
		path, key, version string
		body               map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, version = r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"content":[{"type":"text","text":"{\"change_records\":[]}"}]}`))
	}))
	defer server.Close()

	client := New(Config{Protocol: ProtocolAnthropic, Endpoint: server.URL, APIKey: "sk-ant", Model: "claude-sonnet-4-5"})
	answer, err := client.Generate(context.Background(), "system", "prompt", true, 1.4)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/messages" {
		t.Errorf("the request went to %s", path)
	}
	if key != "sk-ant" || version != anthropicVersion {
		t.Errorf("the request carries key %q and version %q", key, version)
	}
	if body["system"] != "system" {
		t.Errorf("the system prompt is not a field of its own: %v", body)
	}
	// Anthropic's scale stops at 1, where the settings page offers 2.
	if got := body["temperature"].(float64); got != 1 {
		t.Errorf("temperature sent is %v", got)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("the request carries %d messages", len(messages))
	}
	if answer != `{"change_records":[]}` {
		t.Errorf("the answer reads %q", answer)
	}
}

// TestRefusalsCarryTheEndpointsWords covers a wrong key and a model the
// endpoint does not hold: the error says what the endpoint said, and the
// call is not tried again.
func TestRefusalsCarryTheEndpointsWords(t *testing.T) {
	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer server.Close()

	client := New(Config{Protocol: ProtocolOpenAI, Endpoint: server.URL + "/v1", APIKey: "wrong", Model: "gpt-4o-mini"})
	if _, err := client.Generate(context.Background(), "", "prompt", false, 0.1); err == nil {
		t.Fatal("expected a refusal")
	} else if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Errorf("the error reads %q", err)
	}
	if asked != 1 {
		t.Errorf("a refused call was tried %d times", asked)
	}
}

// TestModelsListsWhatAnswers covers the self-check's own call: the model
// list of each protocol, and the tagless name Ollama files under ":latest".
func TestModelsListsWhatAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"qwen3:latest"}]}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"deepseek-chat"}]}`))
	}))
	defer server.Close()

	ollama := New(Config{Protocol: ProtocolOllama, Endpoint: server.URL, Model: "qwen3"})
	models, err := ollama.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !HasModel(models, "qwen3", ProtocolOllama) {
		t.Errorf("Ollama's list %v does not hold qwen3", models)
	}
	if HasModel(models, "qwen3", ProtocolOpenAI) {
		t.Error("a provider's list must match the name it answers to, not Ollama's tags")
	}
	provider := New(Config{Protocol: ProtocolOpenAI, Endpoint: server.URL + "/v1", Model: "deepseek-chat"})
	if models, err = provider.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(models, "deepseek-chat") {
		t.Errorf("the endpoint's list reads %v", models)
	}
}

// TestEndpointTakesTheProtocolsShape covers what a typed endpoint becomes:
// without a scheme it takes http://, a trailing slash goes, and a provider
// endpoint that does not already end in /v1 takes it — including one with a
// path of its own, as MiniMax's "…/anthropic" has. The request paths hang
// off it: "…/anthropic/messages" is a 404, "…/anthropic/v1/messages" an
// answer.
func TestEndpointTakesTheProtocolsShape(t *testing.T) {
	cases := []struct{ protocol, endpoint, want string }{
		{ProtocolOpenAI, "api.deepseek.com", "http://api.deepseek.com/v1"},
		{ProtocolOpenAI, "https://api.moonshot.cn/v1/", "https://api.moonshot.cn/v1"},
		{ProtocolAnthropic, "https://api.anthropic.com", "https://api.anthropic.com/v1"},
		{ProtocolAnthropic, "https://api.minimaxi.com/anthropic", "https://api.minimaxi.com/anthropic/v1"},
		{ProtocolAnthropic, "https://api.minimaxi.com/anthropic/", "https://api.minimaxi.com/anthropic/v1"},
		{ProtocolOpenAI, "https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{ProtocolOllama, "http://127.0.0.1:11434/", "http://127.0.0.1:11434"},
		{ProtocolOpenAI, "", "https://api.openai.com/v1"},
		{ProtocolAnthropic, "", "https://api.anthropic.com/v1"},
	}
	for _, c := range cases {
		client := New(Config{Protocol: c.protocol, Endpoint: c.endpoint, Model: "m"})
		if got := client.Endpoint(); got != c.want {
			t.Errorf("%s %q reads as %q, expected %q", c.protocol, c.endpoint, got, c.want)
		}
	}
}

// TestHasModelCountsTheCase covers a model the endpoint answers to under
// another spelling: MiniMax's list holds "MiniMax-M3" where the settings
// hold "minimax-m3", and both are the same model.
func TestHasModelCountsTheCase(t *testing.T) {
	listed := []string{"MiniMax-M3.1-Flash-Preview", "MiniMax-M3", "MiniMax-M2.7"}
	if !HasModel(listed, "minimax-m3", ProtocolAnthropic) {
		t.Error("a provider's list must match the model in any case")
	}
	if HasModel(listed, "minimax-m2", ProtocolAnthropic) {
		t.Error("a model the list does not hold is not a match")
	}
}
