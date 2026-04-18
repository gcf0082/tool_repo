# tool_repo

一个类似 apt 的简单工具分发服务：在一个目录下摆放二进制/脚本/压缩包，通过 HTTP 分发，客户端一行命令下载或安装。

## Quick start

**服务端：**

```bash
go run . -dir ./packages -port 8080
```

**客户端（任何一台新机器）：**

```bash
curl -fsSL http://host:8080/install_tool_cli | sh      # 装客户端 + 自动配 URL
tool_cli ping                                          # 验证连通
tool_cli install ripgrep                               # 装工具
```

## 目录结构

`packages/` 下必须有一层 `<name>/` 目录，三种布局共存：

```
packages/
├── fzf/                            # B. 无版本 + 平台专属
│   ├── linux-amd64
│   └── linux-amd64.tar.gz
├── ripgrep/                        # C. 有版本 + 平台专属
│   └── 14.1.0/
│       ├── linux-amd64.tar.gz
│       └── darwin-arm64.tar.gz
└── deploy-script/                  # D. 平台无关（文件名即标识）
    ├── 1.2.0
    └── bundle.tar.gz
```

平台命名 Go 风格：`linux-amd64`、`darwin-arm64`、`windows-amd64`。
包格式支持裸文件 + `.tar.gz` / `.tgz` / `.tar.bz2` / `.tbz2` / `.tar.xz` / `.txz` / `.zip` / `.7z`。

## HTTP API

| 端点 | 无参时 | 带参时 |
|---|---|---|
| `GET /` | 返回总览帮助 | — |
| `GET /get_tool?name=<n>[&os=&arch=&version=]` | 返回 `/get_tool` 的详细帮助 | 下载对应包 |
| `GET /install_tool?name=<n>` | 返回 `/install_tool` 的详细帮助 | 返回定制安装脚本 |
| `GET /install_tool_cli` | 永远返回 `tool_cli` 引导脚本；脚本顶部注释即用法说明 | 同左 |

### 解析规则

- `os` + `arch` 必须同时给或同时不给
- 有 `version` → 精确匹配
- 无 `version` → 按 semver 挑最大，非 semver 按字典序兜底
- 同一包同平台多扩展名并存时：裸文件 > `.tar.gz` > 其他
- `/get_tool` 响应带 `Content-Disposition: attachment; filename="..."`，可用 `curl -OJ` 保留文件名

### 示例

```bash
# 用 curl 手动下载
curl 'http://host:8080/get_tool?name=ripgrep&os=linux&arch=amd64' -OJ

# 用 /install_tool 一行装到当前目录
curl -fsSL 'http://host:8080/install_tool?name=ripgrep' | sh

# 用客户端装系统级（见下节）
tool_cli install ripgrep
```

## 服务端运行参数

```bash
tool_repo -dir ./packages -host 0.0.0.0 -port 8080
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `-dir` | `./packages` | 数据目录（`-upstream` 模式下忽略） |
| `-host` | `0.0.0.0` | 绑定 IP |
| `-port` | `8080` | 监听端口 |
| `-upstream` | *(空)* | 若设置则进入转发模式，不再要求 `-dir`；值形如 `http://other:8080` |

## 转发模式（类 nginx 反代）

只当"入口"用、不想在本机维护 `packages/` 时：

```bash
# 源节点（有 packages/）
tool_repo -dir ./packages -port 8080

# 前端入口节点（无 packages/）
tool_repo -upstream http://source-host:8080 -port 9090
```

前端节点行为：

- `GET /get_tool?...` → 反代到上游，原样透传响应头（含 Content-Disposition）
- `GET /install_tool?name=...` → **本地**渲染脚本，`BASE` 指向前端节点自己；脚本回头调前端的 `/get_tool` 再被代理到上游。对最终用户整条链路只有一个入口
- `GET /install_tool_cli` → **本地**渲染（嵌入的 `tool_cli` 不依赖 packages，随服务端二进制走），脚本中的 `set-url` 写入前端节点的 URL
- `X-Forwarded-For` 会被设置；`X-Forwarded-Host`/`X-Forwarded-Proto` 不会，避免上游误把回链指向代理外主机

## 客户端工具 tool_cli

`tool_cli` 是 bash 写的轻量客户端，包住 `curl` 操作，避免每次记 URL。配置位于 `~/.tool_cli/config.json`。

### 一键引导（没有 tool_cli 时）

```bash
curl -fsSL http://host:8080/install_tool_cli | sh
```

服务端把嵌入的 `tool_cli` 装到 **`/usr/local/bin/tool_cli`**（非 root 自动 `sudo`），并用访问者实际用的 URL 自动执行 `set-url`，装完即可用。

自定义安装位置（不用 sudo）：

```bash
curl -fsSL http://host:8080/install_tool_cli | DEST=$HOME/.local/bin/tool_cli sh
```

### 日常使用

```bash
tool_cli help                          # 列出所有命令
tool_cli config                        # 查看 ~/.tool_cli/config.json
tool_cli url                           # 打印当前配置的 URL
tool_cli set-url http://host:8080      # 如果引导时没自动配
tool_cli ping                          # 测试与服务端是否连通

tool_cli get fzf                       # 下载到当前目录；os/arch 自动 uname 推断
tool_cli get ripgrep --version 14.1.0
tool_cli get mytool --os darwin --arch arm64

tool_cli install fzf                   # 等价于 curl /install_tool?name=fzf | sh

# 单次覆盖 URL 不改配置
TOOL_CLI_URL=http://other:9090 tool_cli install fzf
```

依赖：`bash`、`curl`、`python3`（只用来读写 JSON 配置）。

## 构建

```bash
./dist.sh    # 交叉编译 5 个平台到 dist/
```

产物：`tool_repo-linux-amd64`、`-linux-arm64`、`-darwin-amd64`、`-darwin-arm64`、`-windows-amd64.exe`。

## 发布

`.github/workflows/release.yml` 提供手动触发的 Release 流水线：Actions → Release → Run workflow，填 tag（如 `v0.1.0`），会自动跑 `dist.sh` 并把 5 个二进制挂到 [Releases 页](https://github.com/gcf0082/tool_repo/releases)。

## 测试

```bash
go test ./...   # Go 单测 + httptest 端到端（21 个用例）
./test.sh       # 端到端 shell 测试：起服务 + 转发器 + curl 断言 + 真装一次（48 个断言）
```

## License

MIT
