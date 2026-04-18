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
- `-dir`  数据目录，默认 `./packages`
- `-host` 绑定 IP，默认 `0.0.0.0`
- `-port` 端口，默认 `8080`

## 构建

```bash
./build.sh    # 交叉编译 5 个平台到 dist/
```

## 测试

```bash
go test ./...   # 单测 + httptest 端到端
./test.sh       # shell e2e：启动 + curl 断言 + 真装一次
```

## License

MIT
