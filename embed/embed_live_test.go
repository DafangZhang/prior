package embed_test

import (
	"context"
	"os"
	"testing"
	"time"

	"prior/embed"
	"prior/llm"
)

// newLiveClient is a client for the Ollama the machine runs, or a skip.
func newLiveClient(t *testing.T) *embed.Client {
	t.Helper()
	url := os.Getenv("PRIOR_OLLAMA")
	if url == "" {
		url = "http://127.0.0.1:11434"
	}
	c := embed.New(url, "bge-m3:latest", llm.New(llm.Config{Model: "qwen3:latest", Endpoint: url}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Tags(ctx); err != nil {
		t.Skipf("Ollama 不在本机：%v", err)
	}
	return c
}

func TestLiveModels(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	models, err := c.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bge-m3:latest", "qwen3:latest"} {
		if !llm.HasModel(models, want, llm.ProtocolOllama) {
			t.Errorf("模型 %s 不在 %v", want, models)
		}
	}
}

func TestLiveEmbeddingDim(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dim, err := c.Dim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dim != 1024 {
		t.Fatalf("bge-m3 的维度应为 1024，得到 %d", dim)
	}
	vecs, err := c.Embed(ctx, []string{"积分过期规则", "支付渠道路由优先级", "优惠券叠加"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("期望 3 个向量，得到 %d", len(vecs))
	}
	t.Logf("维度 %d，首批向量长度 %d", dim, len(vecs[0]))
}
