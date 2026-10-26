// Package llm calls the language model that writes the analysis, over the
// protocol the settings name: Ollama's own, the OpenAI-compatible chat
// completions API that most providers speak, or the Messages API Claude
// answers.
//
// Embeddings are not here: they stay with Ollama, which embed.Client talks
// to. This package holds the generations, and the model list the settings
// page's self-check reads.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The protocols a model answers over.
const (
	// ProtocolOllama is Ollama's own API, which takes no key.
	ProtocolOllama = "ollama"
	// ProtocolOpenAI is the chat completions API that OpenAI and most
	// providers speak, as DeepSeek, Moonshot, Qwen, OpenRouter and vLLM.
	ProtocolOpenAI = "openai"
	// ProtocolAnthropic is the Messages API Claude answers.
	ProtocolAnthropic = "anthropic"
)

// Protocols are the protocols the settings page offers, in the order it
// shows them. A protocol left empty is Ollama's, which settings saved
// before the choice existed hold.
var Protocols = []string{ProtocolOllama, ProtocolOpenAI, ProtocolAnthropic}

// DefaultNumCtx is the context window an Ollama generation runs with when
// nothing says otherwise. The other protocols have no such setting.
const DefaultNumCtx = 8192

// DefaultTimeout is how long one call may take when nothing says otherwise.
const DefaultTimeout = 10 * time.Minute

// The answer's own bound, where the protocol takes one: Ollama's num_predict,
// and Anthropic's required max_tokens. Without a bound a long document let
// the model generate until it felt finished, and one stage of one review ran
// for ten minutes; with too small a bound a document's answer is cut in the
// middle of its JSON, which loses the stage. A provider answers longer than a
// local model does, so it is given more room; a run can set its own through
// the settings, and a provider that refuses the field is asked once without
// it, where the API's own limit bounds the answer instead.
const (
	// DefaultMaxTokens is the bound an Ollama generation runs with.
	DefaultMaxTokens = 2400
	// DefaultProviderMaxTokens is the bound a provider is asked for.
	DefaultProviderMaxTokens = 8192
)

// maxAnswer is the bound this client asks for, protocol's default where the
// settings say nothing.
func (c *Client) maxAnswer() int {
	if c.cfg.MaxTokens > 0 {
		return c.cfg.MaxTokens
	}
	if c.protocol() == ProtocolOllama {
		return DefaultMaxTokens
	}
	return DefaultProviderMaxTokens
}

// anthropicVersion is the Messages API version the requests ask for.
const anthropicVersion = "2023-06-01"

// DefaultEndpoint is where a protocol answers when no endpoint is set. An
// Ollama model answers where the app's own Ollama URL says, so it has none.
func DefaultEndpoint(protocol string) string {
	switch protocol {
	case ProtocolOpenAI:
		return "https://api.openai.com/v1"
	case ProtocolAnthropic:
		return "https://api.anthropic.com"
	}
	return ""
}

// DefaultModel is the model a protocol starts on, which the settings page
// fills in when the protocol changes: a starting point, not a limit.
func DefaultModel(protocol string) string {
	switch protocol {
	case ProtocolOpenAI:
		return "gpt-4o-mini"
	case ProtocolAnthropic:
		return "claude-sonnet-4-5"
	}
	return "qwen3:latest"
}

// Config is the language model as the settings spell it.
type Config struct {
	// Protocol is ProtocolOllama, ProtocolOpenAI or ProtocolAnthropic.
	Protocol string
	// Endpoint is the base URL: the Ollama server, or the provider's, as
	// "https://api.deepseek.com/v1". A URL without a scheme takes http://,
	// and a provider endpoint without a path of its own takes /v1.
	Endpoint string
	// APIKey is the provider's key: a bearer token for the OpenAI-compatible
	// API, and x-api-key for Anthropic. Ollama takes none.
	APIKey string
	// Model is the model's name at that endpoint.
	Model string
	// NumCtx is the context window an Ollama generation runs with.
	NumCtx int
	// MaxTokens bounds one answer's length; zero takes the protocol's own
	// default. A stage whose answer is cut is asked again over smaller parts,
	// so this is a floor for one call rather than a limit on a review.
	MaxTokens int
	// Timeout is how long one call may take; zero takes DefaultTimeout.
	Timeout time.Duration
}

// Client calls one language model.
type Client struct {
	cfg Config
	// HTTP is the client, with a timeout long enough for a slow model.
	HTTP *http.Client
	// retries is how many times a call that may pass is tried again.
	retries int
}

// New is a client for the model a config names.
func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{cfg: cfg, HTTP: &http.Client{Timeout: timeout}, retries: 3}
}

// Model is the model this client calls.
func (c *Client) Model() string { return c.cfg.Model }

// Protocol is the protocol it calls it over, an empty one being Ollama's.
func (c *Client) Protocol() string { return c.protocol() }

// Endpoint is the base URL its requests go to, as they are sent.
func (c *Client) Endpoint() string { return c.endpoint() }

// Configure applies a run's settings: the context window, and how long one
// call may take before it is cut off. Zero values keep the defaults.
func (c *Client) Configure(numCtx, timeoutSec int) {
	if numCtx > 0 {
		c.cfg.NumCtx = numCtx
	}
	if timeoutSec > 0 {
		if c.HTTP == nil {
			c.HTTP = &http.Client{}
		}
		c.HTTP.Timeout = time.Duration(timeoutSec) * time.Second
	}
}

// Generate asks the model for an answer to a prompt, with the system
// message ahead of it. When json is true the answer is asked to be a JSON
// document, which the analysis's contracts need: over Ollama's format and
// the OpenAI-compatible response_format. Anthropic has no such field, so
// there the request is the prompt's own contract, and the answer is read
// as loosely as the other protocols' answers are.
func (c *Client) Generate(ctx context.Context, system, prompt string, jsonMode bool, temperature float64) (string, error) {
	if c.cfg.Model == "" {
		return "", fmt.Errorf("llm: 未设置语言模型")
	}
	switch c.protocol() {
	case ProtocolOpenAI:
		return c.openai(ctx, system, prompt, jsonMode, temperature)
	case ProtocolAnthropic:
		return c.anthropic(ctx, system, prompt, temperature)
	case ProtocolOllama:
		return c.ollama(ctx, system, prompt, jsonMode, temperature)
	}
	return "", fmt.Errorf("llm: 不认识的协议 %q", c.cfg.Protocol)
}

// Models lists the models the endpoint offers, which is what the settings
// page's self-check reads: it tells a key that works from one that does
// not, and a model the endpoint holds from one it does not.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if c.protocol() == ProtocolOllama {
		var out struct {
			Models []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
		}
		if err := c.call(ctx, http.MethodGet, "/api/tags", nil, &out); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(out.Models))
		for _, m := range out.Models {
			if name := m.Name; name != "" {
				names = append(names, name)
			} else {
				names = append(names, m.Model)
			}
		}
		return names, nil
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/models", nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

// HasModel says whether a list of an endpoint's models holds one: Ollama
// files a tagless name under its ":latest" form, and a provider may list a
// name in another case — MiniMax answers to "minimax-m3" as "MiniMax-M3",
// and its own list spells it the second way.
func HasModel(models []string, model, protocol string) bool {
	if protocol == "" {
		protocol = ProtocolOllama
	}
	for _, m := range models {
		if strings.EqualFold(m, model) {
			return true
		}
		if protocol == ProtocolOllama &&
			(strings.HasPrefix(m, model+":") || strings.TrimSuffix(m, ":latest") == model) {
			return true
		}
	}
	return false
}

// ollama asks Ollama's own API, whose answer is one field.
func (c *Client) ollama(ctx context.Context, system, prompt string, jsonMode bool, temperature float64) (string, error) {
	body := map[string]any{
		"model":  c.cfg.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": temperature,
			"num_ctx":     c.numCtx(),
			"num_predict": c.maxAnswer(),
		},
	}
	if system != "" {
		body["system"] = system
	}
	if jsonMode {
		body["format"] = "json"
	}
	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/generate", body, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("llm: %s", out.Error)
	}
	return strings.TrimSpace(out.Response), nil
}

// openai asks the chat completions API: the system message is the first
// turn, and a JSON answer is asked for with response_format.
func (c *Client) openai(ctx context.Context, system, prompt string, jsonMode bool, temperature float64) (string, error) {
	messages := make([]map[string]string, 0, 2)
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": prompt})
	body := map[string]any{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  c.maxAnswer(),
	}
	// The fields a provider may refuse are the ones it is asked again
	// without, as OpenAI's reasoning models refuse temperature and
	// max_tokens.
	optional := []string{"temperature", "max_tokens"}
	if jsonMode {
		body["response_format"] = map[string]any{"type": "json_object"}
		optional = append(optional, "response_format")
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	err := c.call(ctx, http.MethodPost, "/chat/completions", body, &out)
	var refused *httpError
	if err != nil && errors.As(err, &refused) && refused.status == http.StatusBadRequest {
		// A refused field is not a refused call: the prompt asks for JSON
		// on its own, and the model's own limit bounds the answer.
		for _, field := range optional {
			delete(body, field)
		}
		err = c.call(ctx, http.MethodPost, "/chat/completions", body, &out)
	}
	if err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: %s/chat/completions 没有返回答案", c.endpoint())
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// anthropic asks the Messages API: the system prompt is a field of its own,
// and the answer is a list of content blocks.
func (c *Client) anthropic(ctx context.Context, system, prompt string, temperature float64) (string, error) {
	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": c.maxAnswer(),
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		// Anthropic's scale stops at 1, where the settings page offers 2.
		"temperature": min(temperature, 1),
	}
	if system != "" {
		body["system"] = system
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := c.call(ctx, http.MethodPost, "/messages", body, &out); err != nil {
		return "", err
	}
	var answer strings.Builder
	for _, block := range out.Content {
		if block.Type == "" || block.Type == "text" {
			answer.WriteString(block.Text)
		}
	}
	if answer.Len() == 0 {
		return "", fmt.Errorf("llm: %s/messages 没有返回答案", c.endpoint())
	}
	return strings.TrimSpace(answer.String()), nil
}

// call sends one request, trying again on a failure that may pass: a
// request that never arrived, or an endpoint that is busy or briefly
// broken. What the endpoint itself refused, as a wrong key or a model it
// does not hold, is the caller's to read.
func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	tries := c.retries
	if tries <= 0 {
		tries = 1
	}
	var last error
	for try := range tries {
		if try > 0 {
			pause := time.Duration(1<<uint(try-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
		err := c.once(ctx, method, path, payload, out)
		if err == nil {
			return nil
		}
		last = err
		if ctx.Err() != nil || !retryable(err) {
			return err
		}
	}
	return last
}

// once sends one request and reads the answer.
func (c *Client) once(ctx context.Context, method, path string, payload []byte, out any) error {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint()+path, reader)
	if err != nil {
		return err
	}
	for name, values := range c.headers() {
		req.Header[name] = values
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("llm: 无法连接 %s（%s）：%w", c.endpoint(), path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &httpError{status: resp.StatusCode, url: req.URL.String(), body: string(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("llm: %s 的回答无法解析：%w", path, err)
	}
	return nil
}

// headers are what a protocol's requests carry: the key as a bearer token,
// which is what the OpenAI-compatible API takes, or as Anthropic's
// x-api-key, beside the version its Messages API asks for.
func (c *Client) headers() http.Header {
	h := http.Header{}
	switch c.protocol() {
	case ProtocolAnthropic:
		h.Set("anthropic-version", anthropicVersion)
		if c.cfg.APIKey != "" {
			h.Set("x-api-key", c.cfg.APIKey)
		}
	default:
		if c.cfg.APIKey != "" {
			h.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
	}
	return h
}

// protocol is the protocol in use, an empty one being Ollama's.
func (c *Client) protocol() string {
	if c.cfg.Protocol == "" {
		return ProtocolOllama
	}
	return c.cfg.Protocol
}

// numCtx is the context window an Ollama generation runs with.
func (c *Client) numCtx() int {
	if c.cfg.NumCtx > 0 {
		return c.cfg.NumCtx
	}
	return DefaultNumCtx
}

// endpoint is the base URL the requests hang off: without a scheme a URL
// takes http://, a trailing slash goes, and a provider endpoint that does
// not already end in /v1 takes it — both provider APIs hang their own paths
// off it, and a base of its own is the rule, as MiniMax's
// "https://api.minimaxi.com/anthropic" and DeepSeek's
// "https://api.deepseek.com/v1" both are.
func (c *Client) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(c.cfg.Endpoint), "/")
	if base == "" {
		base = DefaultEndpoint(c.protocol())
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	if c.protocol() != ProtocolOllama && !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}

// httpError is an answer the endpoint refused, with what it said. The URL
// is the one the request went to, which is what says whether an endpoint
// was spelled the way its API wants.
type httpError struct {
	status int
	url    string
	body   string
}

// Error is the endpoint's own words where it sent them, which is what the
// provider APIs do, and the raw answer otherwise.
func (e *httpError) Error() string {
	var message struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.body), &message) == nil && message.Error.Message != "" {
		return fmt.Sprintf("llm: %s 返回 %d：%s", e.url, e.status, message.Error.Message)
	}
	var plain struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(e.body), &plain) == nil && plain.Error != "" {
		return fmt.Sprintf("llm: %s 返回 %d：%s", e.url, e.status, plain.Error)
	}
	if text := strings.TrimSpace(e.body); text != "" {
		return fmt.Sprintf("llm: %s 返回 %d：%s", e.url, e.status, truncate(text, 300))
	}
	return fmt.Sprintf("llm: %s 返回 %d", e.url, e.status)
}

// retryable says a failure may pass by itself: what is not the endpoint
// refusing the request may.
func retryable(err error) bool {
	var refused *httpError
	if !errors.As(err, &refused) {
		return true
	}
	switch refused.status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return refused.status >= 500
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
