# GTI Monitor

GTI Monitor 是一个基于 Go + Vue 的 Git 仓库双向同步服务，适合部署为 Docker 容器。它会周期性检查远端仓库、自动同步远端提交、检测本地未提交改动并自动提交推送，同时提供一个前端管理界面查看和配置监控仓库。

## 功能

- 前端添加和管理监控仓库
- 新增仓库后自动 clone 到 `/app/git/<repo-name>`
- 每个仓库单独配置同步周期
- 支持 SSH 私钥和用户名/密码或 Token 两种认证
- 配置和凭据保存到 `/app/config`
- 凭据使用 AES-GCM 简单加密保存
- 自动定时执行 `fetch -> merge -> auto commit -> push`
- 遇到冲突时优先尝试 Git 自动合并，失败后按“最后修改时间较新优先”策略处理
- 支持手动触发单仓库同步
- GitHub Actions 自动构建并推送镜像到 `ghcr.io`

## 目录约定

容器内目录：

- `/app/git`：被监控的仓库目录
- `/app/bin`：后端二进制
- `/app/html`：前端静态资源
- `/app/config`：配置、凭据和主密钥

仓库源码目录：

- `cmd/server`：服务入口
- `internal/api`：REST API 和静态文件托管
- `internal/config`：配置与凭据持久化
- `internal/gitops`：Git 同步与冲突处理
- `internal/scheduler`：定时任务
- `web`：Vue 前端

## 本地开发

### 1. 启动前端开发模式

```bash
cd web
npm install
npm run dev
```

前端端口可通过参数或环境变量覆盖：

```bash
cd web
VITE_PORT=5175 npm run dev
```

或：

```bash
cd web
npm run dev -- --port 5175
```

### 2. 启动后端

```bash
go run ./cmd/server
```

后端支持参数和环境变量两种配置方式。

默认环境变量：

```bash
GTI_ADDR=:8080
GTI_PORT=8080
GTI_APP_ROOT=/app
GTI_CONFIG_DIR=/app/config
GTI_REPO_ROOT=/app/git
GTI_HTML_DIR=/app/html
```

本地开发时你可以直接指定端口和根目录：

```bash
GTI_PORT=8081 \
GTI_APP_ROOT=$(pwd)/runtime \
GTI_HTML_DIR=$(pwd)/web/dist \
go run ./cmd/server
```

也可以使用参数：

```bash
go run ./cmd/server --port 8081 --app-root $(pwd)/runtime --html-dir $(pwd)/web/dist
```

如果你只想单独覆盖目录，也可以继续使用细粒度环境变量：

```bash
GTI_CONFIG_DIR=./tmp/config
GTI_REPO_ROOT=./tmp/git
GTI_HTML_DIR=./web/dist
```

## Docker 构建与运行

```bash
docker build -t gti-monitor:local .
docker run -d \
  -p 8080:8080 \
  -e GTI_PORT=8080 \
  -e GTI_APP_ROOT=/app \
  -v $(pwd)/runtime/git:/app/git \
  -v $(pwd)/runtime/config:/app/config \
  --name gti-monitor \
  gti-monitor:local
```

然后访问：

```text
http://localhost:8080
```

如果你希望容器以内置的任意非 root 用户运行，例如 `2000:2000`，当前镜像已经会在构建时预创建 `/app`、`/app/git`、`/app/config`，并放开运行期写权限，因此可以直接这样启动：

```bash
docker run -d \
  -p 8080:8080 \
  --user 2000:2000 \
  -v $(pwd)/runtime/git:/app/git \
  -v $(pwd)/runtime/config:/app/config \
  --name gti-monitor \
  ghcr.io/alwaysking/gitmonitor:latest
```

如果你挂载了宿主机目录，仍建议同步保证宿主机目录本身可写：

```bash
mkdir -p ./runtime/git ./runtime/config
chmod -R 777 ./runtime
```

## GitHub Actions 镜像发布

工作流文件位于：

```text
.github/workflows/docker-image.yml
```

行为：

- push 到 `main` 或 `master` 时自动触发
- 自动构建 Docker 镜像
- 自动推送到 `ghcr.io/<owner>/<repo>`

如果仓库是 `owner/GtiMonitor`，镜像地址通常会是：

```text
ghcr.io/owner/gtimonitor:latest
```

## API 概览

- `GET /api/health`
- `GET /api/credentials`
- `POST /api/credentials`
- `GET /api/repositories`
- `POST /api/repositories`
- `PUT /api/repositories/{id}`
- `DELETE /api/repositories/{id}`
- `POST /api/repositories/{id}/sync`

## 冲突处理说明

Git 无法直接保存工作区文件的原始修改时间，所以“按修改时间取新”的实现采用以下策略：

1. 先尝试正常 `git merge`
2. 对无法自动合并的冲突文件：
3. 对比本地当前文件的 `mtime`
4. 同时对比本地分支与远端分支最近一次修改该文件的提交时间
5. 选择时间较新的版本作为最终版本

这是一种实用型策略，适合自动化同步场景，但不等同于人工语义级合并。

## 安全说明

- `/app/config/credentials.json` 中的密钥和密码会加密保存
- `/app/config/master.key` 默认与配置同目录保存
- 你也可以通过 `GTI_MASTER_KEY` 环境变量提供 32 字节 Base64 密钥，避免把主密钥落盘

## 后续可扩展项

- WebSocket 实时日志
- 仓库编辑页
- 多分支策略
- 更细粒度的冲突策略
- 系统通知和告警
