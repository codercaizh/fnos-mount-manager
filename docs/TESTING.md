# Test plan / 测试与验收

[中文](#自动化测试) · [English](#automated-validation)

## 自动化测试

开发环境：Go 1.22+、Node.js 22+、可访问 npm 与官方 fnpack 的 Linux x86_64 构建机。

```bash
npm ci --ignore-scripts
npm run prepare:sdk
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
npm run test:syntax
npx playwright install chromium
npm run test:ui
bash scripts/build.sh
```

| 检查 | 覆盖 |
|---|---|
| 路径/配置 | 本地与远程目录白名单、名称约束、符号链接、目标嵌套、读写模式与无效 JSON |
| 持久化 | JSON 保存与恢复、权限 `0600`、配置损坏和重复记录拒绝 |
| HTTP/API | 允许路由、静态资源 Content-Type、创建参数、错误状态与正确请求头 |
| 网关 CSRF | 内部 Host/浏览器 Origin 不一致、跨站 Fetch Metadata、非 JSON 或缺少自定义头 |
| mount syscall 模拟 | bind 参数、只读 remount、remount 失败回滚、非空目标保护 |
| 映射状态机 | 自动挂载、来源离线、失效挂载、停用卸载、外部挂载冲突与 EBUSY |
| 浏览器 Playwright | 手机宽度白底 UI、官方选择器授权回调模拟、中文路径、读写切换、创建请求 |
| FPK 校验 | 双架构 manifest、ARM64 ELF e_machine=183、x86_64 ELF e_machine=62、图标和 cmd |

**注意：** Go syscall 单测使用替身函数拦截内核 mount/umount；它们不会执行真实特权挂载。Playwright 模拟了宿主的目录回调及本地 JSON API；不能替代 fnOS 真实宿主桥接器集成。

## fnOS 真机验收矩阵（发布前仍需现场验证）

1. **安装与开机**：分别在 ARM64、x86_64 的 fnOS 上手动安装对应 FPK，确认管理员权限、启动与桌面入口，查看 `TRIM_PKGVAR/service.log`。
2. **原生目录选择器**：在 fnOS 网页端、移动客户端打开，来源可以看到“远程挂载”，目标可见本地共享目录，选择路径成功回填。
3. **只读源**：映射含少量非敏感文件的 SMB 测试目录；原版备份应用可以列目录并读取；经映射尝试写入应被拒绝。
4. **读写目标**：在测试目录恢复一份小文件，检查 SMB 原位置内容/大小一致；用自身账号/ACL 检查可写性。
5. **冲突保护**：尝试选择已有非空目标、另一个映射的子目录、symlink 或系统目录，确保拒绝且原数据不变。
6. **映射管理**：执行启用、停用、重命名、重试、删除，检查源文件始终不被删除；占用时 EBUSY 不应强制卸载。
7. **网络故障**：中断远程 CIFS，待 fnOS 原挂载恢复后检查状态与自动重挂。该应用不负责 SMB 重拨。
8. **重启与升级**：系统重启、应用停启及覆盖升级后，配置仍存在，自动恢复记录，并且不会卸载不属于本应用的挂载。
9. **兼容性**：验证原版「备份飞牛」和「备份到飞牛」模式，在真机保存测试结果。

验收失败请提交 Issue，带上 fnOS 版本、`uname -m`、脱敏状态/日志、准确操作步骤。**绝对不要**上传 CIFS 密码、内部认证 cookie、真实私人文件或云端 Token。

## Automated validation

Run the command block above after `npm ci`. Go tests include validation and persistence, handler and CSRF rules, mocked Linux bind mount/remount/unmount, rollback, mount ownership and offline behavior. Playwright performs a mobile-sized UI smoke test with a **simulated native picker callback** and a mocked local API, including create-request serialization.

The `scripts/build.sh` command cross-compiles both ARM64 and x86_64 binaries and produces two architecture-specific FPK files using the official `fnpack` tool. The release workflow also checks the declared manifest platform against each ELF machine type.

**No destructive hardware test is executed in CI.** The nine hardware steps above remain manual tasks on a disposable SMB share and both supported NAS architectures.

## Known verification boundaries

- Simulated bind mount syscalls are not equivalent to a real mount namespace, kernel CIFS behavior or fnOS ACLs.
- No actual SMB network fault injection is run during CI.
- Root capability and startup service behavior depend on fnOS firmware policy; CI cannot certify a particular NAS version.
- No write workload is performed on production shares.

## v1.2.2 / Empty and partial mount metadata responses

Go tests simulate an empty mount-list response and a partial list. Every unmatched mount must continue through the semantic `trim.file.convertPath` fallback, while matched native custom aliases remain unchanged. Browser tests verify the native-alias versus fallback provenance text. fnOS hardware permissions remain unverified.

## v1.2.1 source dialog and alias verification

- The browser smoke test checks the source click **does not** immediately launch the SDK, shows **Choose Folder** / **Choose Remote Directory**, and that the former starts the official fnOS picker.
- The SMB list shows human-readable display labels, not opaque `/vol02/...` paths. Selecting an alias must still submit the **correct original root** as the mapping source.
- Local directory creation happens **only** through the host picker (`creatable:true`); there is no app-specific folder creation button or API.
- Go tests cover mountinfo parsing, `//server/share` fallback labels, decoding fnOS mount-list shapes, exact mountPoint matching (no cross-assignment), custom comments, and semantic path conversion.
- **Real-device test still needed:** confirm `appcgi.mountmgr.list` or `trim.file.convertPath` is permitted to a privileged third-party FPK on this fnOS version and that the actual custom alias matches File Manager. If not available, check fallback displays recognizable share/server information. Confirm folder creation inside the native fnOS picker.

## v1.2.3 / Native on-device config parsing

Go tests create an artificial `mount_info.json` with two CIFS entries, configured aliases and dummy password fields. Tests assert exact mount-point matching (not list order), the intended two-line UI labels, unchanged local metadata and no credentials in serialized API results. Additional tests exercise config array/map wrappers, invalid JSON, missing files, 4 MiB size limit, local-first lookup precedence and API fallback.

A real fnOS test must verify `/etc/mountmgr/mount_info.json` exists on the current firmware and contains mount metadata in one of the supported shapes. Do **not** upload this file, as it may contain passwords.
