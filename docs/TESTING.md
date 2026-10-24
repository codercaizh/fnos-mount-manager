# NAS 工具箱测试与验收 / Testing

[返回 README](../README.md) · [English README](../README_EN.md)

## 自动化测试 / Automated tests

环境：Go 1.23+、Node.js 22+、官方 fnpack 1.2.3（打包时需要）。CI 使用 Ubuntu 24.04。

~~~bash
npm ci --ignore-scripts
npm run prepare:sdk
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
npm run test:syntax
npx playwright install chromium
npm run test:ui

bash scripts/fetch-rclone.sh
bash scripts/build.sh
python3 scripts/verify-fpk.py
~~~

测试使用模拟的 fnOS HTTP 网关、官方文件选择器回调及 Linux 挂载系统调用；**不会在 CI 中触碰真实 NAS 数据或挂载 SMB**。

### 已覆盖的主要场景 / Coverage matrix

| 模块 | 场景 | 自动化检查 |
|---|---|---|
| 映射配置 | 来源/目标校验、同名与同源冲突、SMB 别名解析 | cmd/mountmgr/*_test.go |
| 挂载状态机 | bind/只读 remount、卸载、重试、断线恢复、已有数据保护 | mount_lifecycle_test.go |
| 多级目录恢复 | owned 映射逐级重建、重试幂等、非自有目录拒绝、缺失卷根、symlink/普通文件冲突 | alias_recovery_test.go |
| Web 安全 | 网关 Origin 与内部 Host 不一致、CSRF 自定义头、Content-Type 校验 | api_security_test.go |
| 复制路由兼容 | 标准 /api/copy/pause 与旧 /api/pause 都有明确 JSON 响应，缺少专有标识时拒绝兼容路由 | copy_pause_compat_test.go |
| 任务历史 | 仅导入旧版最近 3 条完成记录、避免重复导入、保留私有记录 | internal/copyengine/engine_test.go |
| 实时文件进度 | rclone 的 checks + transfers 计数、跨来源聚合、重复样本幂等与边界检查 | realtime_files_test.go |
| 多来源并行 | 8 个独立文件最多同时运行 8 个工作位，单来源状态正确持久化 | concurrent_integration_test.go（受环境限制） |
| 目录映射 UI | 官方选择器回调、远程别名、移动端布局、创建和修改交互 | scripts/ui-smoke.cjs |
| 合并 UI | 目录映射/复制切换、最近记录展开后来源/目标路径、来源选择与创建参数 | scripts/ui-toolbox-smoke.cjs |
| 复制手机 UI | 活动文件列表、已完成文件数、长路径、320/360/375/390/412/430px 宽度无横向溢出 | scripts/ui-copy-overflow-smoke.cjs |
| 发布制品 | FPK 内容、manifest 平台、Go/rclone ELF 架构、图标入口、SHA256 | scripts/verify-fpk.py |

独立多文件的并发**隔离集成测试**可在已准备好 /vol99/1000 的测试服务器执行：

~~~bash
RUN_FNOS_PARALLEL_INTEGRATION=1 go test ./internal/copyengine -run TestEightIndependentFilesAreCopiedConcurrently -count=1 -v
~~~

此测试只在测试共享根目录下创建/清理临时文件，使用可观测并发的模拟 rclone 可执行文件，不会连接外部 SMB。**生产设备不需要也不建议运行**。

## fnOS 真机验收（需要人工验证）

1. **覆盖升级**：从原挂载管家升级到 NAS 工具箱，不先卸载。确认原有映射与名称、目标、模式均保留，后台只需一个 mountmgr 服务。
2. **原生选择器**：分别用手机与网页端验证文件单选、多选、文件夹和目标目录选择。不同 fnOS 版本的混选能力可能不同。
3. **远程路径**：先由飞牛连接 SMB；确认两个不同远程目录能映射为同一父目录下的两个独立别名，源与目的不串联。
4. **只读与读写**：使用测试数据验证只读映射的写入被拒绝、读写映射仍受远程 SMB ACL 限制。
5. **离线与恢复**：模拟远程 SMB 断开并由飞牛恢复连接，确认映射状态与自动恢复。工具箱自身不负责 SMB 重拨。
6. **整条目录链重建**：对于 owned=true 的映射，在关闭并解除映射后只清理测试用空挂载点、部分父目录，恢复时应逐级建立；卷根缺失、符号链接、非空目标或 owned=false 必须拒绝。
7. **复制内容**：选择多个独立文件、嵌套目录、中文和特殊字符名称；核对目标大小与目录结构。严格模式可用同大小不同内容的测试文件验证校验逻辑。
8. **并发与进度**：并行数 8，使用大量测试文件核验实际活动传输数、即时字节数、完成文件数、速度与 ETA 是否更新；活动并发不会一直恒等于 8。
9. **暂停、恢复与重启**：复制进行中暂停，确认 UI 和后端状态同步；升级或重启后手动继续，确认已完成文件跳过、中断文件可重新复制，不出现多份任务同时写入。
10. **历史记录**：完成任务只保留最近 3 条，点击展开能看到逐项来源和目标；旧独立快速复制完成记录导入后不重复。
11. **安装包和图标**：ARM64 / x86-64 两架构都要安装验证。退出并重新进入飞牛手机端检查高清图标和页面资源缓存。
12. **数据安全**：确认只执行 copy 而非 sync，不会删除来源；同名目标内容变化时可能重传或覆盖，必须用测试数据验证。

## 已知验证边界 / Known limitations

- Mocked mount syscalls, fnOS gateway and picker are **not the real firmware**. Exact API权限、内核 CIFS 行为、系统升级/卸载脚本及 ACL 仍需真机验证。
- 自动化脚本不会主动断开真实 SMB、操作用户存储卷或执行生产目录的删除操作。
- 大文件字节级断点续传并非此应用保证；部分被中断文件会重新传输。
- GitHub Actions 的构建通过不代表已在每个 fnOS 固件版本的 NAS 上安装成功。
- 复现问题时请提供 fnOS 版本、架构、操作步骤、脱敏日志和测试文件信息；**不要提供 SMB 密码、令牌、私人照片、Cookie 或整份远程挂载配置**。

## English

Automated tests cover mount validation and lifecycle, safe multi-level owned-directory recovery, API and gateway security, legacy/new pause routing, concurrency, file counts from rclone JSON statistics, completed-history import and all relevant mobile UI pages. CI also validates both FPKs and embedded Go/rclone ELF architectures.

Hardware acceptance testing remains necessary for real SMB outages, kernel mount privileges, ACLs, fnOS app gateway behavior, in-place migration, and mobile application caching. Use disposable directories and redact logs before sharing them.
