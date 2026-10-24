# NAS 工具箱（NAS Toolbox）

[English](README_EN.md) · [测试与验收](docs/TESTING.md) · [安全说明](docs/SECURITY.md) · [第三方依赖](docs/THIRD_PARTY.md) · [GitHub Releases](https://github.com/codercaizh/fnos-toolbox/releases)

一个飞牛 fnOS 应用，将原「挂载管家」的**目录映射**与基于 rclone 的**快速复制**合并到一个常驻 Go 服务。两项功能共用应用私有数据目录，不需要为复制任务单独常驻一个后台进程。

**仓库已更名为 [codercaizh/fnos-toolbox](https://github.com/codercaizh/fnos-toolbox)。飞牛应用 ID 和内部 Go 模块路径为兼容旧版仍沿用 `fnos-mount-manager`，无需卸载重装。**

**当前版本：v2.0.7** · ARM64 / x86-64 · fnOS ≥ 1.2.0604 · 需要 root/CAP_SYS_ADMIN（目录映射） · 社区项目，非飞牛官方应用。

## 功能

### 目录映射（原挂载管家）

- 将飞牛「文件管理」已经连接的 SMB/CIFS 远程目录，通过 Linux bind mount 映射为本地共享目录中的独立别名。**映射不是数据复制**，不会下载或同步远程文件。
- 支持只读 / 读写、多个互不冲突的映射、启停、重试、修改来源、重命名与解除映射。
- 应用启动与周期性巡检时恢复映射。若**由本应用创建**的目标目录链消失，允许从已存在的用户存储根目录（例如 /vol1/1000）开始逐级创建缺失目录，然后恢复挂载。
- 不代替飞牛自动重连 SMB，也不会创建缺失的存储卷或用户存储根目录；不会覆盖非空目录、符号链接或其他程序的挂载。

例如：飞牛已挂载远程的「爸爸相册」「妈妈相册」，可以分别映射到 /vol1/1000/网盘备份/爸爸相册 与 /vol1/1000/网盘备份/妈妈相册。

### 快速复制

- 单一飞牛官方文件选择器支持单个文件、多个文件和目录（实际可选类型及混选能力取决于 fnOS 客户端）；目标使用官方目录选择器，可选择已连接的远程挂载子目录。
- 内置 **rclone v1.75.2**。默认并行数为逻辑 CPU 数 × 2，也可以直接填写并行数。
- **快速校验**：按目标相对路径、文件名和大小判断是否跳过；不比较修改时间。
- **严格校验**：使用 rclone 哈希判断差异，并在复制后读取源与目标文件内容进行完整校验。相比快速模式会增加磁盘/网络读写，部分文件系统哈希能力不同。
- 显示累计进度、实时速度、剩余时长（小时/分钟/秒）、活跃传输数与正在传输的文件名，以及实时完成/跳过的文件数。
- 暂停、手动继续、异常重试和重启后任务恢复。**已完成文件按校验规则跳过；正在传输的单个文件不保证字节级断点续传，可能需要重新复制。**
- **统一并发调度：** 同一复制任务内的多个目录、独立文件（包括混选和不同 SMB 挂载）组成临时虚拟来源，**仅启动一个 rclone copy 进程**，全体共享填写的并行数。例如设置 8 路时不再按目录均分，空闲路可复制任意来源的其他文件。只有 1 个待复制文件时仍可能只有 1 路。严格模式的完整内容校验会在复制进程结束后另外顺序执行一次 rclone check，不会并行运行多个进程。
- 「最近传输任务」保留最近 **3 条已完成**记录；运行中、暂停及失败任务仍保留。点击记录可查看每个来源及其目标位置。

目录别名含 `=` 或换行符时，rclone combine 不支持该别名，新任务将返回明确错误而不是写入错误路径。复制运行期间的临时配置与所选独立文件的符号链接保存在应用私有目录中，任务结束即清理，不缓存真实文件内容。中断后按原校验模式重新检查未完成来源，继续复制不保证单文件字节级续传。

**复制不会删除源文件，但目标端同名文件在校验不通过时可能被覆盖。** 复制不等于双向同步，不执行 rclone sync。建议先在测试目录验证。

## 安装与升级

1. 在 [Releases](https://github.com/codercaizh/fnos-toolbox/releases) 下载对应 CPU 架构的 NAS Toolbox FPK；使用飞牛应用中心的「手动安装」功能。
2. 原有「挂载管家」用户**直接覆盖升级，不要先卸载**。应用继续使用旧包 ID **fnos-mount-manager**，以保留原有映射记录。
3. 如果还安装了旧独立「快速复制」，请先完成或暂停旧任务，**暂时不要卸载旧 App**。新版第一次启动时会尝试读取旧应用私有记录，只导入最近 3 条**已完成**任务；不会接管旧版进行中的任务。
4. 确认 NAS 工具箱里的映射与历史记录无误后，再卸载旧独立「快速复制」应用以节省常驻内存。
5. 升级前尽量等待复制任务完成。升级会中断运行中的 rclone 进程；之后可以手动继续。

如果升级后手机显示旧图标或旧页面，完全退出飞牛手机 App 后重新打开。新版桌面入口使用不同的图标资源路径以降低 WebView 缓存影响。

## 使用

**目录映射**：先在飞牛文件管理连接 SMB 远程共享；在「目录映射」页面选择来源、目标共享父目录、别名和读写模式，创建并确认状态为「已挂载」。备份、恢复前请确认源连接在线。

**快速复制**：选择一个或多个来源、目标目录、并行数与校验模式，点击「开始复制」。在「最近传输任务」内可查看正在传输的文件、暂停/继续与详情。网络不稳定或大批量文件时，建议先使用快速模式；需要逐内容复核时选择严格模式。

## 数据存储与技术结构

| 内容 | 位置 / 说明 |
|---|---|
| 应用 ID | fnos-mount-manager（升级兼容） |
| 常驻服务 | 单一 Go 进程 mountmgr，经飞牛网关的 Unix socket 提供 API，不额外开放 TCP 管理端口 |
| rclone | 每个任务同时只运行一个 rclone 子进程（严格模式追加顺序校验）；无任务时不常驻 |
| 映射记录 | TRIM_PKGVAR/private/mounts.json |
| 复制任务 | TRIM_PKGVAR/private/copies.json |
| 服务日志 | TRIM_PKGVAR/service.log |
| 目录映射入口 | 飞牛 App 内「目录映射」 |
| 复制入口 | 飞牛 App 内「快速复制」 |

路径使用官方飞牛存储范围限制；不允许复制来源与目标发生嵌套，避免无限递归；不支持将目录映射目标创建到任意系统路径。完整限制见 [安全说明](docs/SECURITY.md)。

## 从源码构建

环境：Linux x86-64 构建机、Go 1.23+、Node.js 22+、npm、curl、unzip、官方 fnpack 1.2.3；仓库**不提交**第三方 SDK 或 rclone 二进制。

~~~bash
git clone https://github.com/codercaizh/fnos-toolbox.git
cd fnos-toolbox

npm ci --ignore-scripts
npm run prepare:sdk
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
npm run test:syntax
npx playwright install chromium
npm run test:ui

# 拉取并校验官方 rclone 1.75.2 双架构发行包
bash scripts/fetch-rclone.sh

# 官方 fnpack 1.2.3，具体下载地址及 SHA256 见 docs/THIRD_PARTY.md
mkdir -p dist
curl -fsSL https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64 -o dist/fnpack-linux-amd64
echo "54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93  dist/fnpack-linux-amd64" | sha256sum -c -
chmod +x dist/fnpack-linux-amd64
bash scripts/build.sh
python3 scripts/verify-fpk.py
~~~

生成 **dist/nas-toolbox-2.0.7-arm64.fpk**、**dist/nas-toolbox-2.0.7-x86_64.fpk** 及校验文件。CI 对每次推送与 PR 运行 Go 单测、race、vet、Playwright 模拟与双架构构建；**CI 不能替代真机 SMB 断线、挂载权限与系统升级测试**。

## 代码布局

- cmd/mountmgr：映射服务、统一 API 和 Web 前端
- internal/copyengine：rclone 任务、并发、统计、重试与状态持久化
- packaging/fpk：安装包元数据、权限、生命周期脚本及图标
- scripts：SDK 准备、图标生成、rclone 校验下载、双架构打包与 UI 测试
- docs：测试矩阵、安全边界和第三方依赖

原「挂载管家」v1.2.5 的历史详细文档保存在 [README_MOUNT_MANAGER.md](README_MOUNT_MANAGER.md)。

## 安全与声明

目录映射需要 root/CAP_SYS_ADMIN，复制功能也依赖该进程运行；不要将管理接口暴露给非授权用户。不要在 Issue 上传 SMB 密码、云服务凭证、私有相册路径或未经脱敏的日志。请先在无重要数据的目录验证。

本仓库自有代码采用 [MIT](LICENSE)；rclone、官方 fnOS SDK 与 fnpack 各自遵循其原始许可。该项目是独立社区实现，**并非飞牛官方软件**。
