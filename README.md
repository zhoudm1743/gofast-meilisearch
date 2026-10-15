# gofast-meilisearch

GoFast 框架的 [Meilisearch](https://www.meilisearch.com) 全文检索驱动插件。

框架核心 `go-fast-framework` 只定义检索契约（`contracts/search.go`），引擎驱动放外围仓库（模式同 gofast-gorm / gofast-xorm）。本插件基于 Meilisearch HTTP API、仅依赖标准库 `net/http`，零 SDK 依赖。

## 安装

```bash
go get github.com/zhoudm1743/gofast-meilisearch@latest
```

## 接入

```go
import (
    "github.com/zhoudm1743/go-fast-framework/foundation"
    gofastmeili "github.com/zhoudm1743/gofast-meilisearch"
)

app.SetProviders([]foundation.ServiceProvider{
    // ...
    &gofastmeili.ServiceProvider{},
})
```

```yaml
search:
  default: meilisearch
  drivers:
    meilisearch:
      driver: meilisearch
      host: http://localhost:7700
      api_key: masterKey
      primary_key: id      # 可选，默认 "id"
```

## 使用

```go
import (
    "github.com/zhoudm1743/go-fast-framework/contracts"
    "github.com/zhoudm1743/go-fast-framework/facades"
)

// 通过服务容器（或框架侧 Search 门面）获取检索服务
search := app.MustMake("search").(contracts.Search)
index := search.Driver().Index("posts") // 默认驱动；search.Driver("backup") 切换命名驱动

// 新增 / 更新文档（map 或结构体，JSON 序列化）
_ = index.Upsert(map[string]any{"id": 1, "title": "hello", "lang": "zh"})

// 检索
result, _ := index.Search("hello",
    contracts.WithSearchLimit(20),
    contracts.WithSearchFilter("lang = zh"),
    contracts.WithSearchSort("created_at:desc"),
)
for _, hit := range result.Hits {
    _ = hit["id"]
}

// 删除 / 清空
_ = index.Delete(1)
_ = index.Clear()
```

## 语义说明

- **异步 task 语义**：`Upsert` / `Delete` / `Clear` 调用 Meilisearch 后默认异步落库，插件在请求发送成功（2xx）后即返回，不轮询 task 状态。若需强一致读，请在上层自行轮询 Meilisearch task。
- **多驱动**：`search.drivers.<name>` 支持声明多个命名驱动（如 `primary`、`backup`），通过 `search.Driver(name)` 切换；首个驱动可为后续扩展（如 Elasticsearch）预留。
- **主键字段**：默认 `id`，通过 `search.drivers.<name>.primary_key` 配置。
- **认证**：配置 `api_key` 后自动附加 `Authorization: Bearer <api_key>` 头。
