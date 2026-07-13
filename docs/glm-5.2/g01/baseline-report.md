# G01 独立开发基线报告

**日期：** 2026-07-13

**负责人：** 柯大侠

**复核人：** 艾隆

**状态：** 待艾隆 Linux 复核

## 1. 源码基线

- 分支：`feat/glm-5.2-v1.1`
- 上游基线提交：`97762502f25e5e1c84228d13da2af72f7134e276`
- `origin`：`https://github.com/sunecom/atmApi.git`
- 本机原始源码快照未修改。
- 功能分支只增加本 G01 复核包，不包含 GLM 功能代码。

## 2. 交接材料核验

- 艾隆的 schema 有 12 个 `CREATE TABLE`，没有 `INSERT INTO` 或 `REPLACE INTO`。
- 未复制生产数据，未读取或复制 `/tmp/openrouter-key`。
- 交接文档声称 10 张表，schema 实际为 12 张。
- `atmapi-plans.sql` 是 `Unknown column 'models'` 查询错误输出，不是套餐导出。
- 远端 `main` 的已跟踪源码无修改，但 `GLM52-HANDOFF.md` 是未跟踪文件。
- 交接文档中的 Flash/Pro 比例和静默降级方案不采用；GLM-5.2 锁定套餐不得切换到 DeepSeek。

## 3. 隔离环境约定

- Go：1.22.5。
- 测试端口：`13300`，与现有 `3300` 分离。
- 测试数据库标识：`atmapi_glm_test`。
- Windows 预备数据库：`./data/atmapi_glm_test.db`。
- MariaDB 10.5 一致性检查由艾隆 Linux 环境完成。
- G01 不配置渠道 Key。

## 4. 安全发现

基线提交仍跟踪 `.env.alipay` 和 `alipay.env`，且 `main.go` 启动时会主动加载 `.env.alipay` 并覆盖进程环境变量。`internal/config/config.go` 还提供 JWT 默认值，`main.go` 会输出默认管理员账号。

这些问题进入 S01 安全基线任务。在 S01 完成前，G01 只允许编译和测试，禁止启动应用。

## 5. Windows 基线验证

已通过：

- `go version`：`go1.22.5 windows/amd64`
- `go mod verify`：`all modules verified`
- `go test -vet=off ./internal/...`：内部包全部编译通过，无测试文件。
- `CGO_ENABLED=0 go build ./main.go`：通过；构建产物未运行。

修改前既有失败：

1. `internal/model/db.go:104`：`log.Printf` 的 `err` 参数是未调用的函数值，触发 vet 错误。
2. `test/test_phase2a_plus.go` 与 `test/test_phase2c.go` 位于同一包，重复声明 `baseURL`、`main`、`test1`、`Response` 等符号。

上述失败存在于 GLM 修改之前，不应归因于本功能分支。

## 6. 艾隆复核清单

- [ ] 分支和上游基线提交正确。
- [ ] 确认 G01 不需要生产数据库副本。
- [ ] 在 Linux / Go 1.22.5 环境复现并记录基线测试结果。
- [ ] 确认 MariaDB 客户端或服务端版本信息，不连接生产库。
- [ ] 确认测试端口 `13300` 可用。
- [ ] 确认 S01 优先移除并轮换支付、JWT 和管理员凭据。

完成以上复核并把日志路径写入本报告后，G01 方可改为“已完成”。
