# Third-party dependencies / 第三方依赖

The project's original Go daemon, user interface, packaging scripts, icon SVG and tests are licensed under MIT (see root `LICENSE`).

The upstream `@trimjs/web-app@0.5.0` SDK is used for native fnOS folder picker integration. It is installed from npm using the committed lockfile; `scripts/prepare-sdk.cjs` copies its distribution to `cmd/mountmgr/web/fnos-sdk.js` during the build. The generated SDK file is **excluded from git**; the source repository does not claim ownership of it. Upstream terms and notices are governed by the npm package's authors. Review its version and upstream license before redistributing derivative builds.

Official `fnpack 1.2.3` is used as an **external build tool** and is not committed. Download: https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64. Verified SHA-256: `54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93`.

`@resvg/resvg-js` is a development-only icon converter; `playwright` is a development-only browser test dependency; neither is bundled into the runtime.

## 中文

本项目自有 Go、HTML、CSS、JS、脚本与图标采用 MIT。官方 fnOS SDK (`@trimjs/web-app`) 与打包器 `fnpack` 均来自第三方，其版权与分发条款由上游维护者决定；请在再次分发时核查对应授权，不要将其压缩发行版声明为本仓库原创代码。`@resvg/resvg-js` 与 `playwright` 仅用于构建/测试，不进入运行包。
