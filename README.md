# prior · 先例

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey)
[![UI](https://img.shields.io/badge/UI-mygo%20native-6f42c1)](https://github.com/egoist/mygo)
[![License](https://img.shields.io/badge/license-GPLv3-blue)](LICENSE)
[![Status](https://img.shields.io/badge/status-early%20preview-orange)](#已知限制)

**给产品需求文档做跨版本评审的本地桌面工具。**

*A local-first PRD review desk: archive product docs, compare a new requirement document against their history, and put every finding back on a quoted sentence.*

内核 Go，界面 [mygo](https://github.com/egoist/mygo) 原生窗口，没有 WebView；文档、向量与评审结果都在你自己的 PostgreSQL 里。
先把历史 PRD 入库，再把新文档的每条要求回查历史：哪里改了、旧规则有没有交代、前后是否矛盾、哪句话不明确、哪条只有一句话。每条结论都落在原文引用上，能核对，能复制，能写进变更账本。

![报告文档视图](docs/screenshots/report-document.png)

<p align="center"><sub>文档视图：问题在原文高亮，点一下在右侧看结论，不离开原文。</sub></p>

## 目录

[为什么做这个](#为什么做这个) · [功能](#功能) · [界面](#界面) · [快速开始](#快速开始) · [配置](#配置) · [评审流水线](#评审流水线) · [项目结构](#项目结构) · [CLI](#cli) · [实测](#实测) · [开发](#开发) · [已知限制](#已知限制) · [License](#license)

## 为什么做这个

需求文档越写越多，规则散落在不同版本里：

- 新文档把旧规则改掉了，却没写存量数据和老配置怎么办；
- 同一份文档第二节写「24 个月」，第五节又写「长期有效」；
- 一句话需求、模糊指代，开发和测试只能各自理解；
- 评审意见停在聊天记录里，没落到具体句子，过两周就找不到。

prior 把这些变成**带原文引用的发现**，再按预评审结论、严重度、类型和变更账本组织起来。它是本地优先的：文档、向量、评审结果都在你自己的 PostgreSQL 里。

## 功能

| 能力 | 说明 |
|---|---|
| 文档入库 | docx / xlsx / markdown / txt / csv；清洗飞书/Word 导出的 `<span>`、`<br>`、HTML 实体与零宽字符，结构感知切块 |
| 跨文档对照 | 新规则与历史规则冲突、历史有而新文档没交代的覆盖缺口 |
| 文内一致性 | 同一份文档前后阈值/范围/版本/术语矛盾；两处原文都必须能定位 |
| 一句话需求 | 模型之外加规则层兜底，S0 漏抽的「支持导出」「列表增加筛选」也会被标出 |
| 模糊与指代 | 「所有的都不要」「之前的不要」「尽快」「海量」这类没有对象、没有版本、没有数字的表述 |
| 预评审 | 准入定级（可排期 / 需补充 / 打回重写）、工程风险、可直接复制给产品的追问清单 |
| 证据链 | 每条发现带新文档引用；逻辑漏洞/变更信号必须带历史文档与章节，否则降级为待确认问题 |
| 变更账本 | 功能点级的规则时间线，人工确认或否决，支持模块历史 |
| 本地优先 | Go + PostgreSQL + Ollama；没有 WebView，没有云端账号 |

## 界面

### 报告列表

每次运行一行：状态、时间、耗时、发现数、待处理数、预评审结论。点一行打开报告对话框。

![报告列表](docs/screenshots/reports.png)

### 发现清单

按全部 / 变更信号 / 逻辑漏洞 / 不明确 / 一句话需求 / 工程缺口筛选；有证据就双栏对照，没有就不画空栏。

![发现清单](docs/screenshots/report-findings.png)

### 预评审

准入定级、最大工程风险、可直接复制给产品的追问清单。

![预评审](docs/screenshots/report-verdict.png)

### 文档库

文档、日期来源、模块、格式、分块与状态；右侧是文档详情、分块列表与原文预览。

![文档库](docs/screenshots/library.png)

<details>
<summary>深色主题</summary>

![深色主题](docs/screenshots/report-dark.png)

</details>

> 截图使用仓库内置的样例与演示数据生成，不是某个真实客户的知识库。

## 快速开始

### 依赖

- Go 1.27+
- PostgreSQL 16+，可选 [pgvector](https://github.com/pgvector/pgvector)（装了自动走原生向量列与 HNSW）
- [Ollama](https://ollama.com)，用于 embedding（固定 `bge-m3`）
- 评审用的语言模型：Ollama 本地模型，或任意 OpenAI 兼容端点 / Anthropic

```bash
git clone https://github.com/cuczhangyi/prd_viewer.git
cd prd_viewer

# 1. PostgreSQL（端口 5433，避开本机 5432）；库不存在会自动创建
docker compose -f scripts/docker-compose.yml up -d

# 2. 模型
ollama pull bge-m3
ollama pull qwen3     # 或改用 DeepSeek / Moonshot / Qwen / OpenRouter / Anthropic

# 3. 连接自检
go run ./cmd/prior-cli status

# 4. 入库（把 产品文档 换成你的文档目录）
go run ./cmd/prior-cli ingest "产品文档"

# 5. 评审一份新文档
go run ./cmd/prior-cli analyze "产品文档/xxx.docx" -out /tmp/report.md

# 6. 桌面端
go run .
```

首次运行桌面端后，在「设置 → 环境」里保存 PostgreSQL 连接，在「设置 → 分析」里选择语言模型协议、端点和模型。

## 配置

配置有两套入口，互不覆盖：

- **桌面端**：读写 `settings.json`，在「设置」页修改并保存；桌面端**不读取** `PRIOR_*` 环境变量。
- **CLI**：命令行参数 > 环境变量 > 默认值。

### 桌面端 settings.json

配置文件路径（`os.UserConfigDir()/Prior/settings.json`）：

| 系统 | 路径 |
|---|---|
| macOS | `~/Library/Application Support/Prior/settings.json` |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/Prior/settings.json` |
| Windows | `%AppData%\Prior\settings.json` |

首次启动使用默认值；文件损坏会改名为 `settings.json.bak` 并回到默认值；`Version` 比当前版本旧时会整体重置为默认值。

`settings.json` 里与连接、模型相关的键：

| 键 | 默认值 | 说明 |
|---|---|---|
| `Connection.PGDSN` | `postgres://postgres:postgres@127.0.0.1:5433/prior` | PostgreSQL 连接串；设置页「环境」把它拆成主机/端口/库/用户/密码，查询参数原样保留 |
| `Connection.OllamaURL` | `http://localhost:11434` | Ollama 地址；embedding 和 Ollama 协议的语言模型都走它 |
| `Models.Embed` | `bge-m3:latest` | embedding 模型；向量列按 1024 维建，换模型前先确认维度 |
| `Models.Protocol` | `ollama` | `ollama` / `openai` / `anthropic` |
| `Models.LLM` | `qwen3:latest` | 评审语言模型 |
| `Models.Endpoint` | 空 | Provider 基地址；Ollama 协议不需要 |
| `Models.APIKey` | 空 | Provider API Key；文件权限 0600，明文保存，不要提交到仓库 |
| `Models.Temperature` | `0.2` | 0–2，越低越稳定；CLI 的 `-temp` 默认 0.1 |
| `Models.NumCtx` | `8192` | 模型上下文长度；设置页范围 512–32768 |
| `Models.MaxTokens` | `0` | 单次回答上限；0 表示协议默认（Ollama 2400，Provider 8192），不是整次评审上限 |
| `Models.TimeoutSec` | `300` | 单次 HTTP 调用超时，30–3600 秒 |

设置页对应关系：

- **环境**：PostgreSQL 五段连接信息、保存、重新检测；pgvector 可用时自动建扩展与 HNSW 索引，不可用时自动回退到应用侧余弦。
- **分析**：协议、端点、API Key、模型、温度、上下文、回答上限、超时；右上角状态栏会显示当前模型和连接状态。
- **外观 / 数据 / 关于**：主题、语言、侧边栏配色、文档库列显隐、版本信息。

### CLI 参数与环境变量

CLI 的优先级是 **参数 > 环境变量 > 默认值**。环境变量只对 `cmd/prior-cli` 生效。

| 设置 | 参数 | 环境变量 | 默认 |
|---|---|---|---|
| PostgreSQL | `-dsn` | `PRIOR_DSN` | `postgres://postgres:postgres@127.0.0.1:5433/prior` |
| Ollama | `-ollama` | `PRIOR_OLLAMA` | `http://127.0.0.1:11434` |
| embedding 模型 | `-embed` | `PRIOR_EMBED` | `bge-m3:latest` |
| 协议 | `-protocol` | `PRIOR_LLM_PROTOCOL` | `ollama` |
| 端点 | `-endpoint` | `PRIOR_LLM_ENDPOINT` | 协议默认值 |
| API Key | `-llm-key` | `PRIOR_LLM_KEY` | 空 |
| 语言模型 | `-llm` | `PRIOR_LLM` | `qwen3:latest` |
| 温度 | `-temp` | 无 | `0.1` |
| 跳过重排 | `-no-rerank` | 无 | 不跳过 |

### Provider 示例

OpenAI 兼容端点会自动补 `/v1`（如果尚未以 `/v1` 结尾），Anthropic 使用 `x-api-key`，其余 Provider 使用 `Authorization: Bearer`。

| 场景 | 协议 | 端点 | 模型示例 |
|---|---|---|---|
| 本机 Ollama | `ollama` | 用 `Connection.OllamaURL` | `qwen3:latest` |
| DeepSeek | `openai` | `https://api.deepseek.com/v1` | `deepseek-chat` |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-4o-mini` |
| Anthropic | `anthropic` | `https://api.anthropic.com` | `claude-sonnet-4-5` |
| 通义千问 | `openai` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` |
| Moonshot | `openai` | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` |
| OpenRouter | `openai` | `https://openrouter.ai/api/v1` | `deepseek/deepseek-chat` |
| vLLM | `openai` | `http://127.0.0.1:8000/v1` | 按服务端加载的模型名 |

模型名和端点会随服务商调整，以对方文档为准。

### PostgreSQL DSN

```text
postgres://user:password@host:5432/database?sslmode=require
```

- 密码里的 `@`、`/`、`:` 等字符要 percent-encode；设置页的密码框会自动处理。
- 查询参数（如 `sslmode`、`pool_max_conns`）会原样保留。
- 不支持 `key=value` 形式的 DSN 进设置页的拆分字段；这种写法请直接用 CLI `-dsn`。
- 没有 pgvector 也能跑：向量存在 `real[]`，检索在应用侧算余弦；装了则自动用 `vector(1024)` 与 HNSW。

## 评审流水线

```mermaid
flowchart LR
    A[docx / xlsx / md / txt] --> B[ingest<br/>解析 · 清洗 · 切块]
    B --> C[(PostgreSQL<br/>chunks + pgvector)]
    B --> D[bge-m3<br/>embedding]
    A --> E[agent 评审]
    C --> E
    D --> E
    E --> F[S0 需求条目]
    E --> R[检索历史]
    E --> S1[一句话需求]
    E --> S2[明确性 + 规范性]
    E --> P[预评审]
    E --> S3[逻辑漏洞<br/>跨文档 + 文内]
    E --> S4[变更信号 + 台账]
    F --> G[findings<br/>每条带原文引用]
    R --> G
    S1 --> G
    S2 --> G
    P --> G
    S3 --> G
    S4 --> G
    G --> H[报告列表 → 报告对话框<br/>文档视图 · 发现 · 账本 · 模块历史]
```

| 阶段 | 做什么 |
|---|---|
| S0 | 分段读文档，抽取需求条目（含短句）与四元组 |
| R | 标题 + 模块 + 术语 + 章节生成查询，向量/关键词双路召回，RRF 融合，模型重排 |
| S1 | 模型判断不规范条目；规则层补漏短句与无验收标准的要求 |
| S2 | 规则层扫占位符、未随文材料、开放枚举、被动句、模糊交互/性能/量词、无对象范围词与无锚点时间指代；模型层扫语义/隐含假设/边界/数据口径/兼容性 |
| P | 研发视角预评审：准入定级、工程缺口、最大风险、追问清单 |
| S3 | 与历史对照的冲突/覆盖缺口，加文档内部前后不一致 |
| S4 | 变更信号确认；功能点身份解析；写变更账本；旧规则未交代处置的记为未解决冲突 |
| S5 | 去重、校验引用、落库、汇总运行状态 |

## 硬性约定

- **每条发现必须落在新文档的原文片段上**（`span.quote` 必须能在新文档里定位），否则丢弃。
- 逻辑漏洞与变更信号必须引用历史片段与文档；引用无法解析时降级为「不明确」并给出待澄清问题。
- 检索报告的是**证据**（哪份历史文档的哪一段），不是一句结论。
- 界面不显示推断：拿不到真实数据的位置显示空态与原因，不用假数字占位。
- 文内矛盾必须给出两处本文档原文，两处都能定位才算数。

## 项目结构

```
.
├── main.go / app.go / *view.go   # mygo 桌面端（无 WebView）
├── report.go / report_list.go    # 报告列表、报告对话框、左侧菜单
├── report_document.go            # 原文高亮与右侧结论面板
├── ingest/                       # docx / xlsx / markdown / txt 解析、清洗、切块、日期与模块
├── embed/                        # Ollama bge-m3 客户端
├── llm/                          # Ollama / OpenAI 兼容 / Anthropic 协议
├── store/                        # PostgreSQL、pgvector、findings、change_records
├── retrieval/                    # 查询生成、向量+关键词召回、RRF、重排、预算
├── agent/                        # 评审流水线、规则层、预评审、台账、冲突判定
├── cmd/prior-cli/                # survey / ingest / analyze / dates / points / modules / status
├── prd/                          # 产品与实施文档
└── docs/screenshots/             # README 截图
```

产品设计与各轮实施记录见 `prd/PRD-FINAL.md` 与 `prd/round-*.md`。

### 依赖服务

| 服务 | 用途 | 必需 |
|---|---|---|
| PostgreSQL | 文档、块、向量、发现、台账 | 是 |
| pgvector | 原生向量列与 HNSW 索引 | 否，未装时应用侧余弦 |
| Ollama + `bge-m3` | embedding | 是 |
| Ollama / OpenAI 兼容 / Anthropic | 评审语言模型 | 是 |

## CLI

| 命令 | 用途 |
|---|---|
| `prior-cli status` | 连接自检与知识库统计 |
| `prior-cli survey <目录>` | 解析目录，报告格式、日期来源、切块与问题 |
| `prior-cli ingest <目录>` | 入库；`-force` 重读全部，`-ledger` 抽功能点 |
| `prior-cli analyze <文档>` | 评审一份文档；`-out` 写 Markdown，`-no-rerank` 跳过模型重排 |
| `prior-cli modules [目录]` | 重判所属系统；`-apply` 写回 |
| `prior-cli points` | 功能点台账与重复候选；`-merge FROM TO` 人工合并 |
| `prior-cli dates [目录]` | 成稿日期；`-set` 补录，`-rescan` 重读，`-unknown` 只看未知 |

## 实测

本机单张 GPU 的 Ollama，语料约 174 份文档：

| 环节 | 数值 |
|---|---|
| 全量入库 | 172 篇 / 2026 块，约 63s（含 embedding） |
| 单次评审 | 3–5 分钟，6–10 次模型调用（含 S3 跨文档与文内一致性） |
| 查询/召回 | 29 条查询 → 融合约 1000 块 → 重排保留 20 → 4–8 篇历史文档 |
| 向量检索 | 首次 125ms（加载 2026×1024），缓存后 3ms/查询 |
| 关键词检索 | 3ms/查询 |

数字是开发机上的参考值，取决于模型、GPU 和文档长度。

## 开发

```bash
go test ./... -count=1   # 联库/联模型的用例在 PostgreSQL、Ollama 不可达时自动 skip
go vet ./...
```

界面截图：

```bash
PRIOR_SHOTS=1 go test . -run TestScreenshots -count=1
```

截图使用仓库内置的样例与演示数据生成，不读真实知识库。

## 已知限制

- 当前解析 docx / xlsx / markdown / txt / csv；PDF、HTML、扫描件不在解析范围。
- 桌面端报告支持「复制 Markdown 到剪贴板」；桌面端保存文件、历史页的重跑/删除尚未实现（CLI `analyze -out` 可写文件）。
- 单机单用户，没有登录、多租户和权限体系。
- 评审质量取决于所配语言模型；本地小模型在长文档上可能触发截断，运行时会在阶段日志里标出 degraded。
- 首次对旧库使用新版解析器时，建议 `ingest -force` 重读文档并重建 embedding。

## 贡献

欢迎 issue 和 PR。提交前请跑：

```bash
gofmt -l .
go vet ./...
go test ./... -count=1
```

涉及解析、提示词或报告结构的改动，请在 PR 里说明对旧数据的影响（是否需要 `-force` 重读）。

如果它对你的评审流程有帮助，欢迎点一个 Star，或把用得不顺手的地方开成 Issue。

## License

Copyright (C) 2026 cuczhangyi.

[GPL-3.0-only](LICENSE)。

你可以在内部自由使用、修改；对外分发修改版或衍生作品时，需要按 GPLv3 提供对应源码，并保留同样的许可。若你只想允许闭源集成，GPLv3 不是合适的协议。
