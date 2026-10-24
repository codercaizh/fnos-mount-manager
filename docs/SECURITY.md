# Security model / 安全模型

## 中文

挂载管家执行 Linux bind mount、umount，**必须申请 root/CAP_SYS_ADMIN**，属于需要谨慎安装的高权限应用。安装者应当信任完整 FPK 来源并确认 SHA-256。请勿在公共 NAS 上授予普通来宾用户管理访问。

**攻击面和安全措施：**

1. 通过 fnOS 应用网关转发至 `app.sock` Unix socket（应用创建时权限 `0660`）；不暴露 TCP 管理监听端口。访问控制取决于 fnOS 网关与 FPK 的 `allUsers=false` 桌面入口，不应绕过官方授权。
2. 变更 API 必须发送 `Content-Type: application/json` 和 `X-Fnos-Mount-UI: 1`，跨站 Fetch Metadata 被拒绝。考虑到 fnOS 代理重写 Host，合法网页 Origin 不直接与 Unix socket 内部 Host 比较。未向浏览器发送跨域 CORS 授权；任何进入后台的请求仍受 fnOS 网关鉴权约束。这不是用自定义请求头取代用户认证。
3. 只接收本地共享目录或远程挂载路径。通过绝对路径、名称检查和 symlink 拒绝降低目录穿越风险；创建前拒绝非空目标和交叉映射。**注意：** 文件系统路径检查与挂载调用之间仍可能存在同一台 NAS 上其他有写权限程序的并发竞态（TOCTOU），因此本应用不是恶意本地用户隔离边界。
4. 用 Linux mountinfo 的挂载 ID 记录映射归属，避免卸载其它程序的挂载；无法确认归属时采取拒绝而非强制卸载。禁止递归删除文件。来源断线时不假定目标可安全写入。
5. JSON 配置写入私有目录，模式 `0600` 并使用临时文件 + 原子重命名。不要分享 `TRIM_PKGVAR` 下的配置和日志。
6. 只读 bind 限制的是映射出口，**不代表加密、备份完整性或 CIFS 凭证隔离**。实际可写权限仍与 SMB 服务端、fnOS ACL 相关。

报告安全问题请通过 GitHub 的私密漏洞报告功能或维护者的非公开沟通渠道，不要把漏洞利用细节、访问凭证写入公开 Issue。

## English

This is a **privileged** NAS application requiring root/CAP_SYS_ADMIN to perform bind and unmount operations. Install only packages from trusted sources and verify SHA-256 before deployment.

The application listens only on a local Unix socket and relies on the fnOS authenticated gateway. Mutating endpoints require a JSON content type and a custom CSRF header; cross-site Fetch Metadata is rejected, while the browser Origin is not compared against a potentially rewritten internal proxy Host. CORS is not enabled. The custom header is **not a replacement for user authentication**.

Validated local/remote directory roots, strict basename validation, symlink checks, nonempty-destination rejection, and mapping conflict protection help guard accidental data loss. Filesystem validation is nevertheless subject to local TOCTOU races by another process able to modify the same paths, and must not be treated as hostile-local-user sandboxing.

Mount IDs allow the service to identify its own aliases before unmounting, including when the remote source goes offline. Unknown mounts are rejected; source files are never recursively removed. State files are private and atomically persisted (0600).

A read-only alias does not encrypt content or change the underlying SMB credentials or ACLs. Report sensitive security issues privately rather than posting secrets or exploit information publicly.

## Mount configuration confidentiality / 远程挂载配置安全

The privileged service performs a **read-only** (up to 4 MiB) metadata lookup against `/etc/mountmgr/mount_info.json` to show native aliases. This system file may contain stored remote mount credentials. The program projects only allowed fields (`mountPoint`, `comment`, `userName`, `address`); it must never log, return, persist or upload the original JSON, `password` or authentication tokens. The original file is never modified. If lookup fails, safe label fallbacks are used.

应用只读读取飞牛的远程挂载配置，用于匹配别名；配置可能包含 SMB 密码，严禁将完整文件上传至 Issue 或公开讨论，也不得把原始内容记录到日志。应用只返回挂载路径、别名及展示所需的用户/服务器信息。

## NAS Toolbox 2.x 的复制服务 / Copy service security

新版本的 **快速复制**与挂载管理共享同一个 privileged Go 进程及飞牛网关 socket，接口均依赖飞牛官方鉴权。复制变更 API（`/api/copy/create`、`/api/copy/pause`、`/api/copy/resume` 等）另要求 `Content-Type: application/json` 与 `X-Fnos-Quickcopy-UI: 1`，并拒绝明确的跨站 Fetch Metadata 请求。为了兼容旧 WebView，旧版 `/api/pause`、`/api/resume` 仅在含专属复制请求头时转到复制引擎。它们不代替服务端用户鉴权，也不授权第三方跨域访问。

复制仅允许已验证的飞牛用户共享目录 / 远程挂载路径，禁止 source/destination 相互包含、符号链接路径及系统目录。rclone 仅执行 `copy` 或 `copyto`，不会执行 `sync` 或删除源文件；**但存在名称冲突时，已有目标文件可能被覆盖**。如果需要严格一致性校验，复制后执行源/目标完整内容检查，额外消耗网络与磁盘 I/O。应用不保存 SMB 密码。

暂停与重启后的“继续”以完整文件为单位跳过，不保证单文件的分块重传。多个 rclone 子进程最多共享配置的并行预算，管理员仍应根据 NAS 内存、磁盘寿命、网络能力设置合理并行度。缓存的旧页面与新版 API 之间有兼容路由，但推荐升级后关闭再打开客户端。

已完成任务只保留最近三条摘要，**暂停/失败任务仍会保留路径信息**。JSON 记录可能包含个人文件名及 NAS 目录结构，仅存放于私有数据目录；Issue 中的路径与日志需要脱敏。`rclone` 二进制来自校验过 SHA-256 的官方发行包，不作为本仓库自有代码分发。

**English:** Copy endpoints require the same authenticated fnOS gateway plus JSON and a distinct custom request header. A legacy pause/resume route is accepted only with that header. Paths are restricted, symlinks and overlapping trees rejected; rclone copy never deletes sources but can replace conflicting destinations. Task state may contain private filenames. Concurrency and checksums increase resource usage, and a partially transferred individual file may be restarted after interruption.
