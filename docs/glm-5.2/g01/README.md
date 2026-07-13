# G01 复核包

本目录用于让艾隆在 Linux 服务器复核 GLM-5.2 V1.1 的独立开发基线。

## 安全边界

- 不启动 atmApi 服务。
- 不加载 `.env.alipay`、`alipay.env` 或任何渠道 Key。
- 不连接生产数据库，也不复制生产数据。
- 不覆盖现有二进制文件。
- 构建产物和日志只写入 `/tmp/atmapi-g01-review-*`。

## 复核方法

在独立 worktree 的仓库根目录执行：

```bash
bash docs/glm-5.2/g01/verify-linux.sh
```

脚本将检查分支、提交、Go 版本、MariaDB 客户端版本和测试端口，并执行：

```text
go mod verify
go test ./...
go test -vet=off ./internal/...
CGO_ENABLED=0 go build ./main.go
```

`go test ./...` 在基线提交上预计失败，已知原因记录在 [baseline-report.md](baseline-report.md)。复核目标是确认 Linux 环境能够复现这些既有失败，而不是忽略它们。

`test-env.example` 仅说明隔离环境标识。由于当前程序启动时会主动加载仓库中的支付环境文件，S01 完成前禁止据此启动服务。
