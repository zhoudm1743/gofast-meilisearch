package gofastmeili

import (
	"github.com/zhoudm1743/go-fast-framework/contracts"
	"github.com/zhoudm1743/go-fast-framework/foundation"
)

// ServiceProvider Meilisearch 检索服务接入点（可选驱动，需显式加入应用 providers）。
// 将本 Provider 加入应用 providers 后，通过 "search" 服务（或框架侧 Search 门面）使用：
//
//	import gofastmeili "github.com/zhoudm1743/gofast-meilisearch"
//	app.SetProviders(append(providers, &gofastmeili.ServiceProvider{}))
type ServiceProvider struct{}

func (sp *ServiceProvider) Register(app foundation.Application) {
	app.Singleton("search", func(app foundation.Application) (any, error) {
		cfg := app.MustMake("config").(contracts.Config)
		return NewManager(cfg)
	})
}

func (sp *ServiceProvider) Boot(app foundation.Application) error {
	return nil
}
