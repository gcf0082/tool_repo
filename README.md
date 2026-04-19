# tool_repo

一个类似 apt 的简单工具分发服务：在一个目录下摆放二进制/脚本/压缩包，通过 HTTP 分发，客户端一行命令下载或安装。

## Quick start

**服务端：**

```bash
go run . -dir . -port 28080   # 当前目录下要有 packages/ 和/或 scripts/
```

**客户端（任何一台新机器）：**

```bash
curl -fsSL http://host:28080/install_tool_cli | sh      # 装客户端 + 自动配 URL
tool_cli ping                                          # 验证连通
tool_cli install ripgrep                               # 装工具
```

## 目录结构

`-dir <root>` 下约定两个子目录：

```
<root>/
├── packages/    # 二进制分发（下详）
└── scripts/     # shell 脚本（下详）
```

任一存在即可；缺失的那个对应端点会被关闭。

### packages/ 布局

唯一固定 4 级：`<name>/<version>/<os-arch>/<file>`

```
packages/
├── fzf/
│   └── 0.1.0/
│       └── linux-amd64/
│           └── fzf.tar.gz
└── ripgrep/
    ├── 14.0.3/
    │   └── linux-amd64/
    │       └── ripgrep.tar.gz
    └── 14.1.0/
        ├── linux-amd64/
        │   └── ripgrep.tar.gz
        └── darwin-arm64/
            └── ripgrep.tar.gz
```

- 平台命名 Go 风格：`linux-amd64`、`darwin-arm64`、`windows-amd64`
- 叶子文件名**随意**，只需以支持的压缩后缀结尾；裸文件不再接受
- 支持的压缩扩展：`.tar.gz` / `.tgz` / `.tar.bz2` / `.tbz2` / `.tar.xz` / `.txz` / `.zip` / `.7z`
- 同版本同平台下多个文件共存时优先级：`.tar.gz` > `.zip` > `.tar.bz2` > `.tar.xz` > `.7z`

`/get_tool` 请求必须带 `name` + `os` + `arch`；`version` 可选（不给取 semver 最大）。平台无关 / 无版本的场景不再支持（如需要，把脚本放到 `scripts/` 用 `run remote://` 执行）。

## HTTP API

| 端点 | 无参时 | 带参时 |
|---|---|---|
| `GET /` | 返回总览帮助 | — |
| `GET /get_tool?name=<n>[&os=&arch=&version=]` | 返回 `/get_tool` 的详细帮助 | 下载对应包 |
| `GET /install_tool?name=<n>` | 返回 `/install_tool` 的详细帮助 | 返回定制安装脚本 |
| `GET /install_tool_cli` | 永远返回 `tool_cli` 引导脚本；脚本顶部注释即用法说明 | 同左 |
| `GET /get_script?path=<p>` | 返回 `/get_script` 的详细帮助 | 读 `scripts/<p>` 原始内容 |
| `PUT /put_script?path=<p>` (body=脚本) | 返回 `/put_script` 的详细帮助 | 写入 `scripts/<p>`（父目录自动建，同名覆盖） |

### 解析规则

- `name` / `os` / `arch` 必选；`version` 可选
- 无 `version` → 按 semver 挑最大，非 semver 按字典序兜底
- 同一包同版本同平台多文件时：`.tar.gz` > `.zip` > 其他；仍有并列 → 409 Conflict
- `/get_tool` 响应带 `Content-Disposition: attachment; filename="..."`，可用 `curl -OJ` 保留文件名

### 示例

```bash
# 用 curl 手动下载（os/arch 必填）
curl 'http://host:28080/get_tool?name=ripgrep&os=linux&arch=amd64' -OJ

# 用 /install_tool 一行装到当前目录
curl -fsSL 'http://host:28080/install_tool?name=ripgrep' | sh

# 用客户端（os/arch 自动 uname 推断）
tool_cli install ripgrep
```

## 服务端运行参数

```bash
tool_repo -dir . -host 0.0.0.0 -port 28080
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `-dir` | `.` | 数据根目录；下面约定 `packages/` 和 `scripts/` 两个子目录（任一存在即可，另一个缺失则对应端点关闭）。`-upstream` 模式下忽略 |
| `-host` | `0.0.0.0` | 绑定 IP |
| `-port` | `28080` | 监听端口 |
| `-upstream` | *(空)* | 若设置则进入转发模式，不再需要 `-dir`；值形如 `http://other:28080` |

## 转发模式（类 nginx 反代）

只当"入口"用、不想在本机维护 `packages/` 时：

```bash
# 源节点（数据根目录里有 packages/ 和/或 scripts/）
tool_repo -dir /srv/tool_repo -port 28080

# 前端入口节点（不需要 -dir）
tool_repo -upstream http://source-host:28080 -port 9090
```

前端节点行为：

- `GET /get_tool?...` → 反代到上游，原样透传响应头（含 Content-Disposition）
- `GET /install_tool?name=...` → **本地**渲染脚本，`BASE` 指向前端节点自己；脚本回头调前端的 `/get_tool` 再被代理到上游。对最终用户整条链路只有一个入口
- `GET /install_tool_cli` → **本地**渲染（嵌入的 `tool_cli` 不依赖 packages，随服务端二进制走），脚本中的 `set-url` 写入前端节点的 URL
- `X-Forwarded-For` 会被设置；`X-Forwarded-Host`/`X-Forwarded-Proto` 不会，避免上游误把回链指向代理外主机

## 客户端工具 tool_cli

`tool_cli` 是 Go 写的轻量客户端，**无依赖**（静态二进制，不需要 curl / python3）。配置位于 `~/.tool_cli/config.json`。

### 一键引导（没有 tool_cli 时）

```bash
curl -fsSL http://host:28080/install_tool_cli | sh
```

服务端生成的引导脚本做三件事：

1. 根据 `uname` 探测 os/arch，调用 `/tool_cli?os=X&arch=Y` 下载对应平台的二进制（**嵌入**在服务端二进制里，不走 `packages/`）
2. `install -m 755` 到 **`/usr/local/bin/tool_cli`**（非 root 自动 `sudo`）
3. 用访问者实际用的 URL 自动 `tool_cli set-url`

无需额外部署客户端包——服务端二进制里直接带了 5 个平台的 tool_cli。

自定义安装位置（不用 sudo）：

```bash
curl -fsSL http://host:28080/install_tool_cli | DEST=$HOME/.local/bin/tool_cli sh
```

### 日常使用

```bash
tool_cli help                          # 列出所有命令
tool_cli config                        # 查看 ~/.tool_cli/config.json
tool_cli url                           # 打印当前配置的 URL
tool_cli set-url http://host:28080      # 如果引导时没自动配
tool_cli ping                          # 测试与服务端是否连通

tool_cli get fzf                                 # 下载到当前目录；os/arch 自动 uname 推断
tool_cli get ripgrep --version 14.1.0
tool_cli get mytool --os darwin --arch arm64
tool_cli get fzf --dir ~/Downloads               # 下到指定目录（不存在会自动创建）

tool_cli install fzf                             # 下载 + 解压到 /usr/local/bin（默认）
tool_cli install fzf --dir $HOME/.local/bin      # 自定义目录（避免 sudo）

tool_cli run remote://deploy.sh myapp prod       # 流式执行远端脚本，args 传给 $@
tool_cli push ./local.sh remote://my/local.sh    # 上传本地脚本到服务端 scripts/my/local.sh

# 单次覆盖 URL 不改配置
TOOL_CLI_URL=http://other:9090 tool_cli install fzf
```

依赖：无（静态 Go 二进制）。安装行为：`install` 会把 tar.gz / zip 解压到 `--dir`（默认 `/usr/local/bin`），并给可执行文件加 `+x`。

## 远端脚本：执行 / 上传

数据根目录下的 `scripts/`（见"目录结构"一节）支持多级嵌套：

```
<root>/scripts/
├── hello.sh
└── deploy/
    ├── staging.sh
    └── prod.sh
```

### 执行（流式 pipe，不落盘）

```bash
tool_cli run remote://deploy/prod.sh v1.2.0           # args 透传给脚本的 $@
curl -fsSL 'http://host:28080/get_script?path=deploy/prod.sh' | sh -s -- v1.2.0
```

参数传递链路：`tool_cli → curl → sh -s -- args → 脚本($1...$@)`。`--` 防止 `-xxx` 参数被 sh 误解析；`set -o pipefail` 保证 curl 4xx/5xx 时整条命令非零退出。stdin 被管道占用，脚本内不能 `read` 交互输入。

### 上传

```bash
# 裸 curl
curl -T ./local.sh 'http://host:28080/put_script?path=deploy/prod.sh'

# 或用 tool_cli
tool_cli push ./local.sh remote://deploy/prod.sh
```

父目录自动 mkdir；同名覆盖（201 Created / 200 OK）；body 上限 16 MiB。

### ⚠️ 鉴权提示

`/put_script` 当前**无鉴权**。任何能访问该端点的人都能让运行 `tool_cli run` 的机器执行任意 shell。仅在内网/可信环境部署，或自己在前面套反向代理/网络 ACL。token 鉴权列在下一轮路线。

## 构建

```bash
./dist.sh    # 先交叉编译 tool_cli → tool_cli_bin/，再交叉编译 server
```

产物：`dist/tool_repo-<os-arch>[.exe]`（5 平台）。每个服务端二进制都嵌入了 5 份 tool_cli，客户端通过 `/install_tool_cli` 可一键获取。

## 发布

`.github/workflows/release.yml` 提供手动触发的 Release 流水线：Actions → Release → Run workflow，填 tag（如 `v0.1.0`），会自动跑 `dist.sh` 并把 5 个二进制挂到 [Releases 页](https://github.com/gcf0082/tool_repo/releases)。

## 测试

```bash
go test ./...   # Go 单测 + httptest 端到端（21 个用例）
./test.sh       # 端到端 shell 测试：起服务 + 转发器 + curl 断言 + 真装一次（48 个断言）
```

## License

MIT
