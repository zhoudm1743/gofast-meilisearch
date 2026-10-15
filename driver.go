package gofastmeili

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zhoudm1743/go-fast-framework/contracts"
)

// 编译期断言：Driver 满足框架检索驱动契约。
var _ contracts.SearchDriver = (*Driver)(nil)

// Driver Meilisearch 检索驱动（单引擎实例）。
// 通过 HTTP API 与 Meilisearch 交互，仅依赖标准库 net/http。
type Driver struct {
	host       string
	apiKey     string
	primaryKey string
	client     *http.Client
}

// DriverConfig Meilisearch 驱动配置。
type DriverConfig struct {
	// Host Meilisearch 服务地址（如 http://localhost:7700）。
	Host string
	// APIKey 访问密钥；为空则不发送 Authorization 头。
	APIKey string
	// PrimaryKey 文档主键字段名，默认 "id"。
	PrimaryKey string
}

// NewDriver 创建 Meilisearch 驱动。
// client 可选：不传或传 nil 时使用带 10s 超时的默认客户端。
func NewDriver(cfg DriverConfig, client ...*http.Client) (*Driver, error) {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		return nil, fmt.Errorf("[GoFast] gofastmeili: host 不能为空")
	}
	if cfg.PrimaryKey == "" {
		cfg.PrimaryKey = "id"
	}
	c := &http.Client{Timeout: 10 * time.Second}
	if len(client) > 0 && client[0] != nil {
		c = client[0]
	}
	return &Driver{
		host:       host,
		apiKey:     cfg.APIKey,
		primaryKey: cfg.PrimaryKey,
		client:     c,
	}, nil
}

// Index 获取索引操作对象。
func (d *Driver) Index(name string) contracts.SearchIndex {
	return &index{driver: d, name: name}
}

// index 实现 contracts.SearchIndex，绑定单个索引。
type index struct {
	driver *Driver
	name   string
}

// Upsert 新增或更新文档。文档为 map 或结构体（JSON 序列化）。
// Meilisearch 异步落库：发送成功即返回，不轮询 task 状态。
func (i *index) Upsert(documents ...any) error {
	if len(documents) == 0 {
		return nil
	}
	target := i.driver.documentsURL(i.name) + "?primaryKey=" + url.QueryEscape(i.driver.primaryKey)
	_, err := i.driver.do(http.MethodPut, target, documents)
	return err
}

// Search 检索索引。
func (i *index) Search(query string, opts ...contracts.SearchOption) (*contracts.SearchResult, error) {
	o := &contracts.SearchOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	req := searchRequest{Query: query, Limit: o.Limit, Offset: o.Offset, Filter: o.Filter, Sort: o.Sort}

	data, err := i.driver.do(http.MethodPost, i.driver.searchURL(i.name), req)
	if err != nil {
		return nil, err
	}
	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 解析检索响应失败: %w", err)
	}
	if resp.Hits == nil {
		resp.Hits = []map[string]any{}
	}
	return &contracts.SearchResult{
		Hits:  resp.Hits,
		Total: resp.EstimatedTotalHits,
		Took:  time.Duration(resp.ProcessingTimeMs) * time.Millisecond,
	}, nil
}

// Delete 按主键删除文档。同样为异步 task 语义。
func (i *index) Delete(ids ...any) error {
	if len(ids) == 0 {
		return nil
	}
	target := i.driver.documentsURL(i.name) + "/delete-batch"
	_, err := i.driver.do(http.MethodPost, target, ids)
	return err
}

// Clear 清空索引全部文档。
func (i *index) Clear() error {
	_, err := i.driver.do(http.MethodDelete, i.driver.documentsURL(i.name), nil)
	return err
}

// searchRequest 映射 Meilisearch POST /indexes/{uid}/search 的请求体。
type searchRequest struct {
	Query  string   `json:"q"`
	Limit  int      `json:"limit,omitempty"`
	Offset int      `json:"offset,omitempty"`
	Filter string   `json:"filter,omitempty"`
	Sort   []string `json:"sort,omitempty"`
}

// searchResponse 映射检索响应体。
type searchResponse struct {
	Hits               []map[string]any `json:"hits"`
	EstimatedTotalHits int64            `json:"estimatedTotalHits"`
	ProcessingTimeMs   int64            `json:"processingTimeMs"`
}

// documentsURL 拼接 /indexes/{uid}/documents。
func (d *Driver) documentsURL(index string) string {
	return d.host + "/indexes/" + url.PathEscape(index) + "/documents"
}

// searchURL 拼接 /indexes/{uid}/search。
func (d *Driver) searchURL(index string) string {
	return d.host + "/indexes/" + url.PathEscape(index) + "/search"
}

// do 发送请求；非 2xx 响应读取 body 包成 error。
func (d *Driver) do(method, target string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("[GoFast] gofastmeili: 序列化请求体失败: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if d.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.apiKey)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("[GoFast] gofastmeili: %s %s 返回 %d: %s", method, target, resp.StatusCode, truncateBody(data, 200))
	}
	return data, nil
}

// truncateBody 截断响应体摘要，避免错误信息过长。
func truncateBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
