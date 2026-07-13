# G01 独立开发基线报告

**日期：** 2026-07-13

**负责人：** 柯大侠

**复核人：** 艾隆

**状态：** Linux 机器复核通过，待艾隆确认

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

## 6. Linux 机器复核结果

2026-07-13 在艾隆服务器的独立目录 `/home/admin/.openclaw/workspace/atmApi-glm52-review` 执行 `verify-linux.sh`，没有切换或修改原 `atmApi` 工作目录。

- 分支：`feat/glm-5.2-v1.1`
- 复核提交：`e2d5302f631629d21c8ae9c68c1817b42cfd8e35`
- 上游基线提交检查：通过。
- 已跟踪工作树检查：干净。
- Go：`go1.22.5 linux/amd64`。
- MariaDB：`10.5.29-MariaDB`；仅检查版本，没有连接数据库。
- 测试端口 `13300`：未发现监听占用。
- `go mod verify`：通过。
- `go test ./...`：退出码 1，只复现第 5 节登记的既有 vet 和重复声明错误。
- `go test -vet=off ./internal/...`：通过。
- `CGO_ENABLED=0 go build ./main.go`：通过，Linux 构建产物 SHA256 为 `e53d1d7735ef25a1717a83a2d2df01c98ec5852ea7aa3e0b5d5a03fcfe20c10d`。
- 应用启动：否。
- 数据库连接：否。
- 原始日志：服务器 `/tmp/atmapi-g01-review-20260713-113105/verify.log`。
- 本地日志副本 SHA256：`ED98D721A9DE4E853C47AFE010C744326AD0E023253F2CE2AD74D4FCD2BD02A2`。

## 7. 艾隆确认清单

- [x] 分支和上游基线提交正确。
- [ ] 确认 G01 不需要生产数据库副本。
- [x] 在 Linux / Go 1.22.5 环境复现并记录基线测试结果。
- [x] 确认 MariaDB 版本信息，且没有连接生产库。
- [x] 确认测试端口 `13300` 未发现监听占用。
- [ ] 确认 S01 优先移除并轮换支付、JWT 和管理员凭据。

完成以上复核并把日志路径写入本报告后，G01 方可改为“已完成”。

## 8. V0.2.1 Task 1 协议失败基线

2026-07-13，建国签字批准 V0.2.1 开发，Task 0B 继续冻结。Task 1 在提交 `692ff6e` 之后建立脱敏协议夹具和旧止血逻辑特征测试，没有启动应用、加载 `.env`、连接数据库或调用真实上游。

新增夹具覆盖：

- 非流式正文、reasoning-only、tool call、refusal、空 choices、损坏 JSON。
- 流式正文、reasoning-only、分段 tool call、refusal、无 `[DONE]` 的中断流。

特征测试证实：

1. 合法的 `content:null + tool_calls` 响应会被旧字符串判空逻辑识别为失败。
2. 旧全量缓冲转发在上游 EOF 前向客户端输出 0 字节，因此不是真流式。
3. 字符串判空还受 JSON 空格影响；紧凑 JSON 与格式化 JSON 可能产生不同结果，进一步证明必须改为结构化解析。

验证结果：

- `go test -v ./internal/glmoptimizer/...`：通过。
- `go test -vet=off ./internal/...`：通过，包含新增 `internal/glmoptimizer`。
- `go test ./...`：退出码 1，只复现第 5 节登记的两个既有问题：`internal/model/db.go:104` vet 错误，以及 `test_phase2a_plus.go`/`test_phase2c.go` 重复声明。
- 应用健康接口、流式和非流式在线样例：未运行。原因是第 4 节 S01 安全边界仍禁止启动当前应用；Task 1 使用离线协议夹具代替，不以加载敏感环境文件为代价补一项样例。
