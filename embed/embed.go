// Package embed talks to the model services the pipeline runs on: Ollama,
// for the embeddings the knowledge base searches with, and the language
// model the analysis calls, over whatever protocol the settings name. It
// holds no state beyond its clients and never imports ui.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"prior/llm"
)

// Client calls one Ollama server for embeddings, and the language model
// beside it for the calls the analysis makes.
type Client struct {
	// BaseURL is where Ollama answers, as "http://127.0.0.1:11434".
	BaseURL string
	// EmbedModel is the embedding model, as "bge-m3:latest".
	EmbedModel string
	// LLM is the model that writes the analysis: its own protocol, endpoint
	// and key. A nil one leaves every generation to fail with what it is
	// missing.
	LLM *llm.Client
	// Batch is how many texts one embedding call carries.
	Batch int
	// HTTP is the client, with a timeout long enough for a slow model.
	HTTP *http.Client
	// retries is how many times a call is tried again, as the pipeline's
	// rules say three.
	retries int
}

// New is a client for a server, embeddings and the language model beside
// it: the one the analysis calls, whichever protocol answers for it.
func New(baseURL, embedModel string, gen *llm.Client) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		EmbedModel: embedModel,
		LLM:        gen,
		Batch:      16,
		HTTP:       &http.Client{Timeout: 10 * time.Minute},
		retries:    3,
	}
}

// Configure applies the run's model settings: the context window and how
// long a generation may take, which are the language model's, and how long
// an embedding call may take. Zero values keep the defaults.
func (c *Client) Configure(numCtx, timeoutSec int) {
	if c.LLM != nil {
		c.LLM.Configure(numCtx, timeoutSec)
	}
	if timeoutSec > 0 {
		if c.HTTP == nil {
			c.HTTP = &http.Client{}
		}
		c.HTTP.Timeout = time.Duration(timeoutSec) * time.Second
	}
}

// Tags lists the models the server holds.
func (c *Client) Tags(ctx context.Context) ([]string, error) {
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
		name := m.Name
		if name == "" {
			name = m.Model
		}
		names = append(names, name)
	}
	return names, nil
}

// Embed returns the vectors of texts, in batches, retrying a failed call
// with a growing pause.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.EmbedModel == "" {
		return nil, fmt.Errorf("embed: 未设置 embedding 模型")
	}
	batch := c.Batch
	if batch <= 0 {
		batch = 16
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := min(start+batch, len(texts))
		part, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		if len(part) != end-start {
			return nil, fmt.Errorf("embed: 请求 %d 条，返回 %d 条", end-start, len(part))
		}
		out = append(out, part...)
	}
	return out, nil
}

// embedBatch is one call to the embedding endpoint.
func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{"model": c.EmbedModel, "input": texts}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		// Older servers answer one embedding at a time.
		Embedding []float32 `json:"embedding"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/embed", body, &out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) == 0 && out.Embedding != nil {
		return [][]float32{out.Embedding}, nil
	}
	return out.Embeddings, nil
}

// Dim is the dimension of the embedding model, read from one short text:
// the store keeps vectors of one size, and this is how it is known.
func (c *Client) Dim(ctx context.Context) (int, error) {
	vecs, err := c.Embed(ctx, []string{"维度探测 dimension probe"})
	if err != nil {
		return 0, err
	}
	if len(vecs) == 0 {
		return 0, fmt.Errorf("embed: 模型没有返回向量")
	}
	return len(vecs[0]), nil
}

// Generate asks the language model for an answer to a prompt, with the
// system message ahead of it. When json is true the answer is asked to be
// a JSON document, as the analysis's contracts need.
func (c *Client) Generate(ctx context.Context, system, prompt string, jsonMode bool, temperature float64) (string, error) {
	if c.LLM == nil {
		return "", fmt.Errorf("embed: 未设置语言模型")
	}
	return c.LLM.Generate(ctx, system, prompt, jsonMode, temperature)
}

// LLMModel is the name of the language model in use, and "" while none is
// set: a pipeline that has no model to call leaves its stages out.
func (c *Client) LLMModel() string {
	if c.LLM == nil {
		return ""
	}
	return c.LLM.Model()
}

// call posts or gets one endpoint, trying again on a failure that may pass.
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
		if ctx.Err() != nil {
			return err
		}
	}
	return last
}

func (c *Client) once(ctx context.Context, method, path string, payload []byte, out any) error {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("embed: 无法连接 Ollama（%s）：%w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("embed: %s %s 返回 %d：%s", method, path, resp.StatusCode, truncate(string(data), 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("embed: %s 的回答无法解析：%w", path, err)
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
