package gofastmeili

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/zhoudm1743/go-fast-framework/contracts"
)

// 编译期断言：Manager 满足框架检索服务契约。
var _ contracts.Search = (*Manager)(nil)

// Manager 实现 contracts.Search，管理多个命名检索驱动。
//
// 配置结构：
//
//	search:
//	  default: meilisearch
//	  drivers:
//	    meilisearch:
//	      driver: meilisearch
//	      host: http://localhost:7700
//	      api_key: masterKey
//	      primary_key: id
type Manager struct {
	cfg         contracts.Config
	client      *http.Client
	mu          sync.Mutex
	drivers     map[string]contracts.SearchDriver
	defaultName string
}

// NewManager 根据配置创建检索管理器。
// client 可选：不传或传 nil 时使用带 10s 超时的默认客户端。
func NewManager(cfg contracts.Config, client ...*http.Client) (contracts.Search, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	if len(client) > 0 && client[0] != nil {
		c = client[0]
	}
	return &Manager{
		cfg:         cfg,
		client:      c,
		drivers:     make(map[string]contracts.SearchDriver),
		defaultName: cfg.GetString("search.default", "meilisearch"),
	}, nil
}

// Driver 切换到命名驱动；不传或空字符串为默认驱动（search.default）。
// 驱动按需惰性创建并缓存；配置缺失或驱动类型不支持时 panic（与框架
// facades 的 MustMake 语义一致）。
func (m *Manager) Driver(name ...string) contracts.SearchDriver {
	n := m.defaultName
	if len(name) > 0 && name[0] != "" {
		n = name[0]
	}
	return m.resolve(n)
}

// resolve 惰性创建并缓存命名驱动，并发安全。
func (m *Manager) resolve(name string) contracts.SearchDriver {
	m.mu.Lock()
	defer m.mu.Unlock()

	if d, ok := m.drivers[name]; ok {
		return d
	}
	d, err := m.newDriver(name)
	if err != nil {
		panic(err)
	}
	m.drivers[name] = d
	return d
}

// newDriver 按配置构建命名驱动。
func (m *Manager) newDriver(name string) (contracts.SearchDriver, error) {
	prefix := "search.drivers." + name

	driverType := m.cfg.GetString(prefix+".driver", "meilisearch")
	if driverType != "meilisearch" {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 不支持的检索驱动 %q", driverType)
	}
	host := m.cfg.GetString(prefix + ".host")
	if host == "" {
		return nil, fmt.Errorf("[GoFast] gofastmeili: 驱动 %q 缺少 host 配置（%s.host）", name, prefix)
	}
	return NewDriver(DriverConfig{
		Host:       host,
		APIKey:     m.cfg.GetString(prefix + ".api_key"),
		PrimaryKey: m.cfg.GetString(prefix+".primary_key", "id"),
	}, m.client)
}
