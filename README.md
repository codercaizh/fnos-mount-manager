# 挂载管家 · fnOS Mount Manager

[English](README_EN.md) · [测试文档](docs/TESTING.md) · [安全说明](docs/SECURITY.md) · [下载 Releases](https://github.com/codercaizh/fnos-mount-manager/releases)

一个面向 **飞牛 fnOS 的轻量目录映射管理器**。利用 Linux bind mount 将系统已连接的 SMB/CIFS 远程目录映射到本地共享目录，帮助原版「备份」应用访问远程 NAS 数据，**无需修改系统程序**。

![移动端界面预览](docs/screenshot-mobile.png)

> **v1.2.2** · ARM64 / x86_64 · fnOS ≥ 1.2.1604 · MIT（自有代码） · 安装需 root/CAP_SYS_ADMIN 特权。请先在测试目录验证兼容性。

## 使用场景与原理

原版飞牛备份选择器有时不能直接选择远程挂载目录。例如：

| 设置 | 路径示例 |
|---|---|
| fnOS 已经连接的远程 SMB 源 | `/vol02/1000-0-<mount-id>/Photos` |
| 本地共享父目录 | `/vol1/1000/share` |
| 映射名称 | `爸爸相册` |
| 最终本地别名路径 | `/vol1/1000/share/爸爸相册` |

这个别名是**同一批远程数据的另一个访问入口**，不是复制、同步，也不负责重新拨号连接 SMB。

## 功能

- **原生目录选择器**：使用 fnOS `@trimjs/web-app` 官方 SDK，来源可选择远程挂载，目标只允许本地共享目录。
- **两种访问模式**：只读用于备份来源，读写用于需要回写远程 NAS 的恢复流程。
- **自动维护**：启动时及每 60 秒检测 bind mount；来源恢复上线后自动尝试重挂。
- **多条映射**：新建、重命名、启用、停用、状态检查、删除，保留持久化配置。
- **数据保护**：拒绝非空目标、符号链接、系统目录、交叉嵌套路径及覆盖其他程序的挂载；正常卸载不会删除源数据。
- **资源占用轻量**：Go 单二进制，独立 fnOS FPK，无额外数据库、容器或公网服务端口。

## 安装和操作

1. 从 [Releases](https://github.com/codercaizh/fnos-mount-manager/releases) 下载 **arm64** 或 **x86_64** 对应的 FPK。
2. fnOS「应用中心 → 手动安装」，选择对应 FPK 并启动「挂载管家」。
3. 提前在 fnOS 系统**远程挂载**中连接好目标 SMB/CIFS 共享。
4. 选择来源目录（例如 `/vol02/1000-0-xxx/Photos`）及本地父目录（例如 `/vol1/1000/share`）。
5. 输入映射名称，设置 **只读 / 读写**，点击「创建映射」。
6. 状态应为「已挂载」，在原版备份应用「我的文件」下选择新的本地目录。

**只读与读写：** 只读 remount 仅作用于新的绑定别名，不会改变原始 CIFS 挂载的全局权限。选择读写也不能越过 SMB 服务器端的访问控制和 fnOS ACL。

**管理操作：** 停用会卸载别名，启用会恢复巡检；删除只移除映射记录和本应用创建的空目录，不递归删除源文件。重命名、停用、删除前应停止正在使用该路径的备份或还原任务，避免 EBUSY（设备忙）。

## 限制与安全

- 本应用**不会建立或重连 CIFS 网络会话**。SMB 断开时由 fnOS 远程挂载功能负责恢复；重连后应用巡检才重新建立本地路径别名。
- 只检查 Linux `/proc/self/mountinfo`，不持续探测 SMB 服务器在线情况。因此网络已断开但内核还保留挂载项时，状态可能暂时滞后。
- 不支持对来源内部的独立子挂载执行递归绑定（non-recursive bind）。
- 目录范围明确限定：远程来源 `/vol02/<mount-id>/...`，本地目标 `/volN/<数字用户ID>/<共享目录>/...`，禁止操作任意系统目录。
- 特权申请使用 fnOS FPK 的 `run-as=root`；如果 fnOS 拒绝第三方特权应用，请遵守系统限制。
- 已通过自动化后端、接口、浏览器和打包校验；**不能据此宣称所有 fnOS 固件上的真机安装、写入权限和开机重挂测试均已通过**。详见 [测试文档](docs/TESTING.md)。

## 从源码构建

需要 Go 1.22+、Node.js 22+、npm、Linux x86_64（作为编译主机）及官方 `fnpack 1.2.3`。构建同时生成两种架构的 FPK：

```bash
git clone https://github.com/codercaizh/fnos-mount-manager.git
cd fnos-mount-manager

npm ci --ignore-scripts
npm run prepare:sdk
go test ./...
go test -race ./...
go vet ./...
npm run test:syntax

# 浏览器 UI 冒烟测试
npx playwright install chromium
npm run test:ui

# 官方 fnpack 下载并校验
mkdir -p dist
curl -fsSL https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64 -o dist/fnpack-linux-amd64
echo '54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93  dist/fnpack-linux-amd64' | sha256sum -c -
chmod +x dist/fnpack-linux-amd64

bash scripts/build.sh
```

输出：`dist/fnos-mount-manager-1.2.2-arm64.fpk`、`dist/fnos-mount-manager-1.2.2-x86_64.fpk` 及对应 SHA-256。两个 FPK 的 manifest 平台分别是 `arm` 与 `x86`，包含不同的 Linux ELF 可执行文件。

## 源码目录

```text
cmd/mountmgr/main.go            HTTP API / 守护 / bind mount
cmd/mountmgr/*_test.go          Go 单元测试和系统调用模拟
cmd/mountmgr/web/               白底 Web 页面与 fnOS 原生选择器
packaging/fpk/                  fnOS manifest、权限、脚本及图标
scripts/build.sh                双架构交叉编译与官方 fnpack
scripts/prepare-sdk.cjs         固定 SDK 版本并复制至嵌入资源
scripts/ui-smoke.cjs            Playwright 测试页面和选择器回调
scripts/icon.svg                应用图标源文件
.github/workflows/ci.yml        自动测试、构建与发布
docs/                           测试、安全、第三方依赖说明
```

应用配置：`TRIM_PKGVAR/private/mounts.json`（0600 权限，原子写入）。应用日志：`TRIM_PKGVAR/service.log`。管理页面由 fnOS 网关访问 Unix socket `TRIM_APPDEST/app.sock`，不监听外部 TCP 管理端口。

## 许可与声明

仓库自有代码采用 [MIT](LICENSE)。fnOS、`@trimjs/web-app` 和 `fnpack` 为各自权利人的项目；SDK 通过锁定 npm 依赖重建，不将第三方压缩代码冒称自有源码，具体见 [第三方说明](docs/THIRD_PARTY.md)。本项目并非飞牛官方应用。欢迎提交附带 fnOS 版本、CPU 架构及脱敏日志的 Issue。

## v1.2.2：明确区分飞牛别名与 SMB 共享名

此前 fnOS 挂载接口返回空列表或缺少部分挂载别名时，会提前停止名称解析，导致显示 `共享名 · IP`。现在会继续调用飞牛 `trim.file.convertPath` 尝试转换未取得别名的路径，并保留已经匹配到的自定义别名。

SMB 弹窗会明确标识名称来源：**飞牛挂载别名**、**飞牛文件路径名称**或**SMB 共享地址（未获取系统别名）**。升级不会改变挂载路径或配置。

注意：此修复不保证所有固件允许第三方应用读取原生别名。若仍显示“未获取系统别名”，需对照飞牛文件管理器的实际别名来诊断。

## v1.2.1：来源两步选择与 SMB 挂载别名

点击 **来源目录**，会先弹出两个选项：

- **选择文件夹**：进入飞牛官方目录选择器，可选择共享中的具体子文件夹。
- **选择远程目录**：打开当前在线的 SMB 共享列表，可以直接选择整个共享根目录（包括空 SMB 共享）。列表优先使用飞牛挂载管理元数据中的自定义别名（`comment`），并按飞牛文件管理器方式附上 `用户名@服务器`。应用只将真实路径用于内部映射，不在列表中展示 `/vol02/...` 哈希路径。

系统挂载元数据优先经 fnOS API Scope 的 `appcgi.mountmgr.list` 查询；如果固件拒绝该调用，再尝试 `trim.file.convertPath` 语义路径转换；仍不可用时会显示内核 CIFS 挂载来源的“共享名 · 服务器地址”。降级名称可能与文件管理器自定义别名不同，实际兼容性需在对应固件上验证。所有查询均不向前端返回 SMB 密码。

**本地目录** 只保留飞牛官方 `pickFile({ directory:true, creatable:true })` 选择器，移除应用自己额外的“新建文件夹”按钮和对应高权限 API。只有宿主支持该参数时，才能在官方弹窗中直接新建文件夹；独立网页授权模式的支持取决于 fnOS 实现。

## v1.2.3：从飞牛本机配置读取真实 SMB 挂载别名

如果飞牛 API 不允许第三方应用读取挂载别名，之前会显示类似“我的照片-243836522 · 10.0.2.50”的 SMB 共享名称，无法区分你在飞牛文件管理器中设置的“妈妈相册”和“爸爸相册”。

**现在新增了更可靠的优先数据源：** 对于已申请 root 权限的挂载管家，后台将只读解析飞牛 mountmgr 使用的 `/etc/mountmgr/mount_info.json`，提取挂载点 `mountPoint`、用户自定义 `comment`、`userName` 和 `address`，通过**准确的真实挂载点**映射到在线 SMB 共享。界面展示为与飞牛文件管理器一致的两行：

- **妈妈相册** · `myp@10.0.2.50`
- **爸爸相册** · `caizh@10.0.2.50`

这里只是根据所给截图举例，并不会写死用户别名或账号。所有其它 SMB 挂载也自动从自身配置读取。**不会修改 fnOS 配置文件；不会将 SMB 密码传给网页或日志。** 如果系统文件缺失或格式不同，仍回退到原版 API 的别名/语义转换，最后才显示“共享名 · 服务器”。

此应用仍未在当前 NAS 的实际配置文件上运行测试；若升级后仍回退，请提供 fnOS 固件版本和列表显示来源（不要上传含 SMB 密码的完整配置文件）。

## v1.2.4：修复挂载配置多层 JSON 导致系统别名解析失败

v1.2.3 虽然尝试只读读取 `/etc/mountmgr/mount_info.json`，但读取器只支持单层映射和数组，未处理飞牛在实际配置中使用的 **用户 ID → 挂载 ID → 挂载记录** 两级对象结构。因此当文件存在时也会忽略所有有效条目，继续显示底层 SMB 共享名。

新版按深度限制安全遍历嵌套 JSON，通过叶子节点的 `mountPoint` 与 `comment` 识别挂载记录，并将 `comment`、`userName`、`address` 投影到只允许显示的字段。仍使用**准确挂载点**匹配，不依赖列表顺序，不修改系统配置，不记录密码字段。

新增完全复现该双层结构的回归测试、三条跨用户挂载匹配、防止别名串号、嵌套非挂载对象过滤与深度限制测试。依据：[实际 fnOS 挂载配置遍历示例](https://www.wangzipei.cn/archives/fei-niu-jiang-gua-zai-pan-gua-zai-dao-ben-di-mu-lu)。

**提醒：** 依然需要安装到真实 fnOS 才能确认当前固件字段格式；只在 n2 构建机上执行了脱敏模拟测试。
