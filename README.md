# tool_repo

一个类似 apt 的简单工具分发服务：在一个目录下摆放二进制/脚本/压缩包，通过 HTTP 分发，客户端一行命令下载或安装。

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

- `GET /` — 概览
- `GET /get_tool?name=<n>[&os=&arch=&version=]` — 下载
- `GET /install_tool?name=<n>` — 返回定制安装脚本

无参数访问任一端点都会返回该端点的帮助。

### 示例

```bash
# 下载（带 Content-Disposition，可用 -OJ）
curl 'http://host:8080/get_tool?name=ripgrep&os=linux&arch=amd64' -OJ

# 一行安装到当前目录
curl -fsSL 'http://host:8080/install_tool?name=ripgrep' | sh
```

## 解析规则

- `os` + `arch` 必须同时给或同时不给
- 有 `version` → 精确匹配
- 无 `version` → 按 semver 挑最大，非 semver 按字典序兜底
- 同一包同平台多扩展名并存时：裸文件 > `.tar.gz` > 其他

## 运行

```bash
go run . -dir ./packages -host 0.0.0.0 -port 8080
```

参数：
- `-dir`       数据目录，默认 `./packages`（`-upstream` 模式下忽略）
- `-host`      绑定 IP，默认 `0.0.0.0`
- `-port`      端口，默认 `8080`
- `-upstream`  转发模式：把业务请求代理到另一个 tool_repo

## 转发模式（类 nginx 反代）

当你只想要一个入口、不想在本机维护 `packages/` 时，用 `-upstream`：

```bash
# 源节点（有 packages/）
tool_repo -dir ./packages -port 8080

# 前端入口节点（无 packages/）
tool_repo -upstream http://source-host:8080 -port 9090
```

在前端节点上发的请求：

- `GET /get_tool?...` → 反代到上游，原样透传响应（含 Content-Disposition）
- `GET /install_tool?name=...` → 本地渲染脚本，`BASE` 指向前端节点自己；脚本回头调前端的 `/get_tool`，再被代理到上游。对最终用户而言，整个链路的入口永远是前端节点
- `X-Forwarded-For` 会被设置，`X-Forwarded-Host`/`X-Forwarded-Proto` 不会（避免上游误把回链指向代理外主机）

## 客户端工具 tool_cli

`tool_cli` 是 bash 写的轻量客户端，包住 `curl` 操作，避免每次记 URL。配置放在 `~/.tool_cli/config.json`。

### 一键引导（没有 tool_cli 时）

```bash
curl -fsSL http://host:8080/install_tool_cli | sh
```

服务端把嵌入的 `tool_cli` 脚本装到 **`/usr/local/bin/tool_cli`**（非 root 自动用 sudo），并用访问者实际用的 URL 自动执行 `set-url`，装完即可用。

自定义安装位置（比如不要 sudo）：

```bash
curl -fsSL http://host:8080/install_tool_cli | DEST=$HOME/.local/bin/tool_cli sh
```

### 日常使用

```bash
./tool_cli set-url http://host:8080    # 如果引导时没自动配
./tool_cli ping                        # 测试与服务端是否连通
./tool_cli get fzf                     # os/arch 自动 uname 推断
./tool_cli get ripgrep --version 14.1.0
./tool_cli get mytool --os darwin --arch arm64
./tool_cli install fzf                 # 一行安装到当前目录

# 单次覆盖 URL 不改配置
TOOL_CLI_URL=http://other:9090 ./tool_cli install fzf
```

依赖：`bash`、`curl`、`python3`（只用来读写 JSON 配置）。

## 构建

```bash
./dist.sh    # 交叉编译 5 个平台到 dist/
```

## 测试

```bash
go test ./...   # 单测 + httptest 端到端
./test.sh       # shell e2e：启动 + curl 断言 + 真装一次
```

## License

MIT
