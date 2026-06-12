# lclient 安装指南（Linux）

`lclient` 依赖 [`go-curl-impersonate`](https://github.com/TeamMilestone/go-curl-impersonate) → [`libcurl-impersonate`](https://github.com/lexiforest/curl-impersonate)。本文档讲怎么在 Linux 上把它们装齐。

> ✅ 已验证：Ubuntu 22.04 / Debian 12 / CentOS Stream 9 / Rocky Linux 9 / Alpine 3.19
> ✅ 架构：x86_64（amd64） / aarch64（arm64）

---

## ⚡ TL;DR 一把梭（Debian / Ubuntu x86_64）

如果你只想最快跑通，复制粘贴下面这一整段。其它发行版往后看 Step 1 选对应命令。

```bash
# 1) 装基础工具 + 运行时依赖 + dev 依赖（编译机一次装齐）
sudo apt update
sudo apt install -y \
  build-essential pkg-config curl ca-certificates patchelf \
  libcurl4-openssl-dev libidn2-dev libzstd-dev libnghttp2-dev libbrotli-dev zlib1g-dev \
  libidn2-0 libzstd1 libnghttp2-14 libbrotli1 zlib1g

# 2) 下载 libcurl-impersonate（走国内镜像）
cd /tmp
VER=1.2.2
IMP_ARCH=$(uname -m)
FILE="libcurl-impersonate-v${VER}.${IMP_ARCH}-linux-gnu.tar.gz"
curl -LO "https://ghfast.top/https://github.com/lexiforest/curl-impersonate/releases/download/v${VER}/${FILE}"

# 3) 解压 + 拷到 /usr/local/lib + 刷 ldconfig
mkdir -p libimp-extract && tar -xzf "$FILE" -C libimp-extract
sudo mkdir -p /usr/local/lib
sudo mv libimp-extract/libcurl-impersonate.* /usr/local/lib/
echo '/usr/local/lib' | sudo tee /etc/ld.so.conf.d/usr-local-lib.conf
sudo ldconfig

# 4) 链接 curl 头文件到 /usr/local/include（Debian/Ubuntu 走 multiarch 路径）
sudo mkdir -p /usr/local/include
if   [ -d /usr/include/x86_64-linux-gnu/curl ]; then
  sudo ln -sf /usr/include/x86_64-linux-gnu/curl /usr/local/include/curl
elif [ -d /usr/include/aarch64-linux-gnu/curl ]; then
  sudo ln -sf /usr/include/aarch64-linux-gnu/curl /usr/local/include/curl
elif [ -d /usr/include/curl ]; then
  sudo ln -sf /usr/include/curl /usr/local/include/curl
fi

# 5) 手工建 pkg-config 描述文件
sudo mkdir -p /usr/local/lib/pkgconfig
sudo tee /usr/local/lib/pkgconfig/libcurl-impersonate.pc > /dev/null <<'EOF'
prefix=/usr/local
exec_prefix=${prefix}
libdir=${exec_prefix}/lib
includedir=${prefix}/include

Name: libcurl-impersonate
Description: libcurl with browser impersonation
Version: 1.2.2
Libs: -L${libdir} -lcurl-impersonate
Cflags: -I${includedir}
EOF

# 6) 配环境变量到 ~/.bashrc
cat >> ~/.bashrc <<'EOF'

# libcurl-impersonate
export PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:${PKG_CONFIG_PATH}"
export LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}"
export LIBRARY_PATH="/usr/local/lib:${LIBRARY_PATH}"
EOF
source ~/.bashrc

# 7) 验证
pkg-config --libs libcurl-impersonate    # 期望: -L/usr/local/lib -lcurl-impersonate
ldconfig -p | grep impersonate           # 期望: libcurl-impersonate.so.4 => /usr/local/lib/...
ls /usr/local/include/curl/curl.h        # 期望: 文件存在
```

跑完所有 7 步后 `go run main.go` 应该就过了。如果中途卡住，往下看分步说明。

---

## 环境要求

| 组件 | 版本 |
|---|---|
| Linux | glibc 2.31+（Ubuntu 20.04+ / Debian 11+ / CentOS 8+）或 musl（Alpine 3.16+） |
| Go | 1.21+ |
| GCC / clang | 任意 |
| pkg-config | 0.29+ |

---

## Step 1：基础工具

```bash
# Ubuntu / Debian
sudo apt update
sudo apt install -y build-essential pkg-config curl ca-certificates patchelf

# CentOS / Rocky / RHEL
sudo dnf install -y gcc pkgconf curl ca-certificates patchelf

# Alpine（musl）
sudo apk add build-base pkgconf curl ca-certificates patchelf bash
```

---

## Step 2：装 Go（从官网，不要发行版仓库里的旧版）

```bash
GO_VER=1.25.0
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  *) echo "不支持的架构 $ARCH"; exit 1 ;;
esac
curl -LO "https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "go${GO_VER}.linux-${GOARCH}.tar.gz"

# 加到 PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

go version   # 期望 go1.25.0
```

---

## Step 3：装系统依赖（运行时 + dev 包）

libcurl-impersonate 需要 4 个 C 库做 IDN / HTTP/2 / Brotli / zstd 解压。**编译机必须同时装运行时包和 dev 包**。

| 包类别 | 装什么 | 谁用 |
|---|---|---|
| **运行时**（`.so.N`） | `libzstd1` `libidn2-0` `libnghttp2-14` `libbrotli1` `zlib1g` | 二进制运行时 `dlopen` |
| **dev**（`.so` 软链 + `.h`） | `libzstd-dev` `libidn2-dev` `libnghttp2-dev` `libbrotli-dev` `zlib1g-dev` `libcurl4-openssl-dev` | cgo 编译期 `-lzstd` 等 |

### Ubuntu / Debian

```bash
# 编译机：运行时 + dev 一次装齐
sudo apt install -y \
  libcurl4-openssl-dev libidn2-dev libzstd-dev libnghttp2-dev libbrotli-dev zlib1g-dev \
  libidn2-0 libzstd1 libnghttp2-14 libbrotli1 zlib1g

# 纯运行时容器（不在容器里编译）：只装运行时
# sudo apt install -y libidn2-0 libzstd1 libnghttp2-14 libbrotli1 zlib1g
```

### CentOS / Rocky / RHEL

```bash
# 编译机
sudo dnf install -y \
  libcurl-devel libidn2-devel libzstd-devel libnghttp2-devel brotli-devel zlib-devel \
  libidn2 libzstd libnghttp2 brotli zlib

# 纯运行时
# sudo dnf install -y libidn2 libzstd libnghttp2 brotli zlib
```

### Alpine（musl）

```bash
# 编译机
sudo apk add \
  curl-dev libidn2-dev zstd-dev nghttp2-dev brotli-dev zlib-dev \
  libidn2 zstd-libs nghttp2-libs brotli-libs zlib

# 纯运行时
# sudo apk add libidn2 zstd-libs nghttp2-libs brotli-libs zlib
```

### 为什么需要 dev 包？

cgo 在 link 阶段会用 `-lzstd -lidn2` 这种短名。链接器找的是**没版本号的** `libzstd.so` / `libidn2.so`，这种"裸 .so 软链"只在 dev 包里。仅装运行时（`libzstd1`）会报：

```
/usr/bin/ld: cannot find -lzstd: No such file or directory
```

---

## Step 4：下载 libcurl-impersonate

```bash
cd /tmp
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  IMP_ARCH=x86_64 ;;
  aarch64) IMP_ARCH=aarch64 ;;
  *) echo "不支持的架构 $ARCH"; exit 1 ;;
esac
VER=1.2.2

# glibc 系统（Ubuntu / Debian / CentOS / Rocky）
FILE="libcurl-impersonate-v${VER}.${IMP_ARCH}-linux-gnu.tar.gz"
# musl（Alpine）改成：
# FILE="libcurl-impersonate-v${VER}.${IMP_ARCH}-linux-musl.tar.gz"

# 国内服务器走镜像，依次尝试直到成功
ORIG_URL="https://github.com/lexiforest/curl-impersonate/releases/download/v${VER}/${FILE}"
MIRRORS=(
  "https://ghfast.top/${ORIG_URL}"
  "https://gh-proxy.com/${ORIG_URL}"
  "https://github.moeyy.xyz/${ORIG_URL}"
  "https://mirror.ghproxy.com/${ORIG_URL}"
  "${ORIG_URL}"   # 兜底直连 GitHub
)
for url in "${MIRRORS[@]}"; do
  echo "尝试: $url"
  if curl -L --connect-timeout 10 --retry 1 -fO "$url"; then
    echo "✓ 下载成功"
    break
  fi
done

# 验证文件大小（4~25 MB 是合理范围；几百字节是 HTML 错误页）
ls -lh "$FILE"

# 解压 + 拷到 /usr/local/lib
mkdir -p libimp-extract
tar -xzf "$FILE" -C libimp-extract
sudo mkdir -p /usr/local/lib
sudo mv libimp-extract/libcurl-impersonate.* /usr/local/lib/

# 验证：应看到 5 个文件
ls /usr/local/lib/ | grep impersonate
# libcurl-impersonate.a
# libcurl-impersonate.la
# libcurl-impersonate.so          (软链 → .so.4)
# libcurl-impersonate.so.4        (软链 → .so.4.8.0)
# libcurl-impersonate.so.4.8.0    (真实文件)
```

### 国内镜像源对照

| 镜像 | URL 前缀 | 稳定性 |
|---|---|---|
| **ghfast.top** ⭐ | `https://ghfast.top/<原 GitHub URL>` | 当前最稳，速度快 |
| **gh-proxy.com** | `https://gh-proxy.com/<原 GitHub URL>` | 备用 |
| **github.moeyy.xyz** | `https://github.moeyy.xyz/<原 GitHub URL>` | 备用 |
| **mirror.ghproxy.com** | `https://mirror.ghproxy.com/<原 GitHub URL>` | 老牌，偶尔失效 |
| 直连 GitHub | 原始 URL | 境外服务器走这条 |

> 镜像源会更换 / 失效，本文档发布时 ghfast.top 速度最稳。如果都不行，搜"GitHub 加速 2026"找新的。

---

## Step 5：注册到 ldconfig

Linux 用 ldconfig 维护一个全局动态库索引。把 `/usr/local/lib` 加进搜索路径：

```bash
echo '/usr/local/lib' | sudo tee /etc/ld.so.conf.d/usr-local-lib.conf
sudo ldconfig

# 验证：应看到 .so.4 路径
ldconfig -p | grep impersonate
# libcurl-impersonate.so.4 (libc6,x86-64) => /usr/local/lib/libcurl-impersonate.so.4
# libcurl-impersonate.so   (libc6,x86-64) => /usr/local/lib/libcurl-impersonate.so
```

---

## Step 6：链接 curl 头文件到 `/usr/local/include`

发行版的头文件位置不一样，统一软链到 `/usr/local/include`，便于我们 Step 7 的 `.pc` 引用：

```bash
sudo mkdir -p /usr/local/include

# 自动探测（Debian/Ubuntu multiarch 优先）
if   [ -d /usr/include/x86_64-linux-gnu/curl ]; then
  sudo ln -sf /usr/include/x86_64-linux-gnu/curl /usr/local/include/curl
elif [ -d /usr/include/aarch64-linux-gnu/curl ]; then
  sudo ln -sf /usr/include/aarch64-linux-gnu/curl /usr/local/include/curl
elif [ -d /usr/include/curl ]; then
  sudo ln -sf /usr/include/curl /usr/local/include/curl
else
  echo "⚠ 找不到 curl 头文件目录，跑 'find /usr -name curl.h' 看看在哪"
fi

# 验证
ls /usr/local/include/curl/curl.h
```

### 各发行版头文件实际位置

| 发行版 | 路径 | 命令验证 |
|---|---|---|
| **Debian / Ubuntu**（multiarch） | `/usr/include/x86_64-linux-gnu/curl/` ⭐ | `dpkg -L libcurl4-openssl-dev \| grep curl.h` |
| **CentOS / Rocky / RHEL** | `/usr/include/curl/` | `rpm -ql libcurl-devel \| grep curl.h` |
| **Alpine** | `/usr/include/curl/` | `apk info -L curl-dev \| grep curl.h` |

> Debian/Ubuntu 用 multiarch 是踩坑高发地。装了 `libcurl4-openssl-dev` 但 `/usr/include/curl/` 不存在是正常现象。

---

## Step 7：建 `.pc` 描述文件（关键）

`TeamMilestone/go-curl-impersonate` 通过 `#cgo pkg-config: libcurl-impersonate` 找库，但**预编译包不带 `.pc`**。手工建一个：

```bash
sudo mkdir -p /usr/local/lib/pkgconfig

sudo tee /usr/local/lib/pkgconfig/libcurl-impersonate.pc > /dev/null <<'EOF'
prefix=/usr/local
exec_prefix=${prefix}
libdir=${exec_prefix}/lib
includedir=${prefix}/include

Name: libcurl-impersonate
Description: libcurl with browser impersonation
Version: 1.2.2
Libs: -L${libdir} -lcurl-impersonate
Cflags: -I${includedir}
EOF

# 验证
pkg-config --libs libcurl-impersonate
# 期望: -L/usr/local/lib -lcurl-impersonate

pkg-config --cflags libcurl-impersonate
# 期望: -I/usr/local/include
```

---

## Step 8：配置环境变量

| 变量 | 作用 | 影响 |
|---|---|---|
| `PKG_CONFIG_PATH` | pkg-config 搜索路径 | 编译期：cgo 通过它找 `.pc` |
| `LIBRARY_PATH` | gcc/g++ 链接器搜索路径 | 编译期：`-lcurl-impersonate` 解析 |
| `LD_LIBRARY_PATH` | 运行时动态库加载路径 | 运行期：进程启动时 `dlopen` |

写到 `~/.bashrc`（zsh 用户改 `~/.zshrc`）：

```bash
cat >> ~/.bashrc <<'EOF'

# libcurl-impersonate
export PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:${PKG_CONFIG_PATH}"
export LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}"
export LIBRARY_PATH="/usr/local/lib:${LIBRARY_PATH}"
EOF

source ~/.bashrc
echo "PKG_CONFIG_PATH=$PKG_CONFIG_PATH"
echo "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"
echo "LIBRARY_PATH=$LIBRARY_PATH"
```

> ⚠ **不要重复加**：如果之前 `cat >> ~/.bashrc` 加过，再加一次会复制三行。编辑文件检查，只留一份。

### systemd service / cron / 非交互式 shell

`~/.bashrc` 只对交互式 shell 生效。systemd 服务要在 unit 文件里加：

```ini
[Service]
Environment=PKG_CONFIG_PATH=/usr/local/lib/pkgconfig
Environment=LIBRARY_PATH=/usr/local/lib
Environment=LD_LIBRARY_PATH=/usr/local/lib
```

cron 任务在脚本开头加 `source /etc/profile` 或显式 export。

---

## Step 9：自检脚本

存为 `lclient-doctor.sh`：

```bash
#!/usr/bin/env bash
ok()   { echo "  ✅ $*"; }
fail() { echo "  ❌ $*"; FAIL=1; }

echo "=== Go ==="
if go version >/dev/null 2>&1; then
  ok "$(go version)"
else
  fail "go 不在 PATH 里"
fi

echo
echo "=== libcurl-impersonate.so ==="
if [ -f /usr/local/lib/libcurl-impersonate.so.4 ]; then
  ok "$(ls -l /usr/local/lib/libcurl-impersonate.so.4 | awk '{print $NF}')"
else
  fail "libcurl-impersonate.so.4 缺失（回到 Step 4）"
fi

echo
echo "=== ldconfig 缓存 ==="
if ldconfig -p 2>/dev/null | grep -q libcurl-impersonate; then
  ok "$(ldconfig -p | grep libcurl-impersonate | head -1 | xargs)"
else
  fail "ldconfig 没找到 libcurl-impersonate（跑 sudo ldconfig；或检查 /etc/ld.so.conf.d/usr-local-lib.conf）"
fi

echo
echo "=== pkg-config ==="
if pkg-config --exists libcurl-impersonate; then
  ok "libs:   $(pkg-config --libs libcurl-impersonate)"
  ok "cflags: $(pkg-config --cflags libcurl-impersonate)"
else
  fail "pkg-config 没找到 libcurl-impersonate（.pc 文件缺；或 PKG_CONFIG_PATH 没设）"
fi

echo
echo "=== curl 头文件 ==="
if   [ -f /usr/local/include/curl/curl.h ]; then
  ok "/usr/local/include/curl/curl.h（软链）"
elif [ -f /usr/include/curl/curl.h ]; then
  ok "/usr/include/curl/curl.h"
elif [ -f /usr/include/x86_64-linux-gnu/curl/curl.h ]; then
  fail "在 multiarch 路径 /usr/include/x86_64-linux-gnu/curl/，软链没建。跑：
       sudo ln -sf /usr/include/x86_64-linux-gnu/curl /usr/local/include/curl"
elif [ -f /usr/include/aarch64-linux-gnu/curl/curl.h ]; then
  fail "在 multiarch 路径 /usr/include/aarch64-linux-gnu/curl/，软链没建。跑：
       sudo ln -sf /usr/include/aarch64-linux-gnu/curl /usr/local/include/curl"
else
  fail "curl.h 缺失（sudo apt install libcurl4-openssl-dev 或对应发行版的 -devel）"
fi

echo
echo "=== 链接期裸 .so（dev 包）==="
for lib in libcurl.so libzstd.so libidn2.so libnghttp2.so libbrotlidec.so libz.so; do
  if find /usr/lib /usr/lib64 /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu -maxdepth 1 -name "$lib" 2>/dev/null | grep -q "$lib"; then
    ok "$lib"
  else
    fail "$lib 缺失（装对应的 -dev / -devel 包）"
  fi
done

echo
echo "=== 运行时 .so.N ==="
for lib in libidn2.so.0 libzstd.so.1 libnghttp2.so.14 libbrotlidec.so.1 libz.so.1; do
  if ldconfig -p 2>/dev/null | grep -q "$lib"; then
    ok "$lib"
  else
    fail "$lib 缺失"
  fi
done

echo
echo "=== 环境变量 ==="
[ -n "$LD_LIBRARY_PATH" ]  && ok "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"   || fail "LD_LIBRARY_PATH 未设（Step 8）"
[ -n "$LIBRARY_PATH" ]     && ok "LIBRARY_PATH=$LIBRARY_PATH"         || fail "LIBRARY_PATH 未设（Step 8）"
[ -n "$PKG_CONFIG_PATH" ]  && ok "PKG_CONFIG_PATH=$PKG_CONFIG_PATH"   || fail "PKG_CONFIG_PATH 未设（Step 8）"

echo
if [ -z "$FAIL" ]; then
  echo "🎉 全部检查通过，可以跑 go run 了"
else
  echo "❗ 有未通过项，按上面的提示修复"
  exit 1
fi
```

```bash
chmod +x lclient-doctor.sh
./lclient-doctor.sh
```

---

## Step 10：最小可运行 demo

```go
// hello.go
package main

import (
    "fmt"

    "crawler-article/tools/request/lclient"
)

func main() {
    s := lclient.NewSession(lclient.WithImpersonate(lclient.Chrome131))
    defer s.Close()

    rep, err := s.CheckFingerprint()
    if err != nil {
        panic(err)
    }
    fmt.Printf("JA3:    %s\nJA4:    %s\nAkamai: %s\nUA:     %s\n",
        rep.JA3Hash, rep.JA4, rep.Akamai, rep.UserAgent)
}
```

```bash
go run hello.go
```

期望输出（具体 hash 会随 chrome 版本变化）：

```
JA3:    8d2e957d3bb44b8e2e4a9ad9d20c1cae
JA4:    t13d1517h2_8daaf6152771_b0da82dd1658
Akamai: 1:65536;3:1000;4:6291456;6:262144|15663105|0|m,p,a,s
UA:     Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36
```

4 个字段都非空即环境 OK。

---

## Docker 部署模板

多阶段构建，runtime 镜像最小化：

```dockerfile
# ============ build stage ============
FROM golang:1.25-bookworm AS build

# 编译期依赖：pkg-config + 全部 -dev 包
RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config curl ca-certificates \
    libcurl4-openssl-dev libidn2-dev libzstd-dev libnghttp2-dev libbrotli-dev zlib1g-dev \
 && rm -rf /var/lib/apt/lists/*

ARG IMP_ARCH=x86_64
ARG IMP_VER=1.2.2
# 大陆构建传 --build-arg GH_MIRROR=https://ghfast.top/
ARG GH_MIRROR=""

RUN curl -L -o /tmp/libimp.tar.gz \
      "${GH_MIRROR}https://github.com/lexiforest/curl-impersonate/releases/download/v${IMP_VER}/libcurl-impersonate-v${IMP_VER}.${IMP_ARCH}-linux-gnu.tar.gz" \
 && mkdir -p /tmp/libimp \
 && tar -xzf /tmp/libimp.tar.gz -C /tmp/libimp \
 && mv /tmp/libimp/libcurl-impersonate.* /usr/local/lib/ \
 && mkdir -p /usr/local/lib/pkgconfig \
 && printf 'prefix=/usr/local\nexec_prefix=${prefix}\nlibdir=${exec_prefix}/lib\nincludedir=${prefix}/include\n\nName: libcurl-impersonate\nDescription: libcurl with browser impersonation\nVersion: %s\nLibs: -L${libdir} -lcurl-impersonate\nCflags: -I${includedir}\n' "$IMP_VER" \
        > /usr/local/lib/pkgconfig/libcurl-impersonate.pc \
 && ln -sf /usr/include/x86_64-linux-gnu/curl /usr/local/include/curl \
 && echo '/usr/local/lib' > /etc/ld.so.conf.d/usr-local-lib.conf \
 && ldconfig

ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig \
    LIBRARY_PATH=/usr/local/lib \
    LD_LIBRARY_PATH=/usr/local/lib

WORKDIR /src
COPY . .
RUN go build -o /out/app ./cmd/your-cmd

# ============ runtime stage ============
FROM debian:bookworm-slim

# 运行时只装 .so.N，不要 -dev
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates libidn2-0 libzstd1 libnghttp2-14 libbrotli1 zlib1g \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /usr/local/lib/libcurl-impersonate.* /usr/local/lib/
RUN echo '/usr/local/lib' > /etc/ld.so.conf.d/usr-local-lib.conf && ldconfig

ENV LD_LIBRARY_PATH=/usr/local/lib

COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
```

构建命令：

```bash
# 境外
docker build -t myapp .

# 大陆
docker build --build-arg GH_MIRROR=https://ghfast.top/ -t myapp .
```

要点：
- **build 阶段**装全部 `-dev` 包，包括 `libcurl4-openssl-dev libidn2-dev libzstd-dev libnghttp2-dev libbrotli-dev zlib1g-dev`
- **runtime 阶段**只装运行时 `.so.N`，不带 `-dev`
- 两阶段都跑 `ldconfig`

---

## 故障对照表

| 报错 | 阶段 | 原因 | 修法 |
|---|---|---|---|
| `Package libcurl-impersonate was not found` | cgo / pkg-config | `.pc` 文件缺或 `PKG_CONFIG_PATH` 没设 | Step 7 + Step 8 |
| `'curl/curl.h' file not found` | cgo 编译 | 没装 `libcurl4-openssl-dev` / `libcurl-devel` | Step 3 |
| 装了 dev 包但 `/usr/include/curl/curl.h` 不存在 | cgo 编译 | Debian/Ubuntu multiarch 路径 | Step 6（`dpkg -L libcurl4-openssl-dev \| grep curl.h` 确认） |
| `cannot find -lcurl-impersonate` | link | `LIBRARY_PATH` 没设 / `.so` 不在 `/usr/local/lib` | Step 4 + Step 8 |
| `cannot find -lzstd` / `-lidn2` / `-lnghttp2` / `-lbrotli` / `-lz` | link | 只装了运行时包没装 -dev | Step 3 装 `-dev` 包 |
| `error while loading shared libraries: libcurl-impersonate.so.4` | 运行时 | `LD_LIBRARY_PATH` 没设 / ldconfig 没刷 | Step 5 / Step 8 |
| `Symbol not found: _idn2_*` | 运行时 | `libidn2-0` 缺失 | `sudo apt install libidn2-0` |
| `Symbol not found: _ZSTD_*` | 运行时 | `libzstd1` 缺失 | `sudo apt install libzstd1` |
| Alpine 上 `Error loading shared library`  | 运行时 | glibc 包跑在 musl 上 | 改用 `linux-musl` tar 包 |
| `pkg-config: command not found` | 编译 | 系统缺 pkg-config | `sudo apt install pkg-config` / `sudo dnf install pkgconf` |
| 跑通了但 ja3 跟 Go 默认 client 一致 | 业务 | profile 没生效 | 确认调了 `lclient.WithImpersonate(...)` |
| systemd 跑起来报库找不到 | 运行时 | unit 文件没设环境变量 | Step 8 末尾的 `[Service] Environment=` |
| 镜像下载报 HTML 错误页 / 几百字节文件 | 下载 | 该镜像挂了 | 换 Step 4 列表里的下一个 |

---

## 卸载

```bash
sudo rm -f /usr/local/lib/libcurl-impersonate.*
sudo rm -f /usr/local/lib/pkgconfig/libcurl-impersonate.pc
sudo rm -f /etc/ld.so.conf.d/usr-local-lib.conf
sudo rm -f /usr/local/include/curl    # 只删软链，不影响 apt 装的 curl
sudo ldconfig
```

`~/.bashrc` 里手动删掉那三行 export。

---

## 与 macOS 安装的关键差异

| 项 | macOS | Linux |
|---|---|---|
| 动态库后缀 | `.dylib` | `.so` |
| 动态库搜索 | `DYLD_LIBRARY_PATH` + `install_name` | `LD_LIBRARY_PATH` + `ldconfig` |
| 修 install_name | 必须 `install_name_tool` | 不需要（用 ldconfig） |
| Gatekeeper / 签名 | `xattr -dr com.apple.quarantine` + `codesign` | 不需要 |
| 包管理 | Homebrew | apt / dnf / yum / apk |
| 头文件 | brew 装 curl 即带 | 必须装 `-dev` / `-devel` 子包 |
| 头文件路径 | `/opt/homebrew/opt/curl/include/curl/`（固定） | 因发行版而异，Debian 走 multiarch |
| dev / runtime 分离 | 通常不分（brew 包就是合一的） | apt / dnf 严格区分 |
| 预编译 tar 包 | `*-macos.tar.gz` | `*-linux-gnu.tar.gz`（glibc）或 `*-linux-musl.tar.gz`（Alpine） |

---

## 维护说明

- libcurl-impersonate 升级到新版本时，把 `VER=1.2.2` 改成新版本号重跑 Step 4 即可。`.pc` 里的 `Version:` 字段记得同步改。
- 切换架构（x86_64 ↔ aarch64）需要重新下载对应 tar 包，并把 Step 6 软链改成对应的 multiarch 路径。
- 不要直接改 `/usr/local/lib/libcurl-impersonate.so.4`；如果要换版本，先 `sudo rm`，再走 Step 4。
