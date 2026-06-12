# lclient 安装指南（macOS / Apple Silicon）

> 已在 macOS Sonoma + M 系列芯片上验证通过。Intel Mac 路径相同，只把 `arm64-macos` 换成 `x86_64-macos`。

## 环境要求

| 组件 | 版本 |
|---|---|
| macOS | 11.0+ |
| Go | 1.21+ |
| Xcode Command Line Tools | 任意 |
| Homebrew | 任意 |

如果还没装：

```bash
# Xcode CLT
xcode-select --install

# Homebrew
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

---

## Step 1：装 Go 和系统依赖

```bash
brew install go pkg-config curl libidn2 zstd
```

其中：
- `curl`：用它的头文件 `curl.h`（编译期需要）
- `libidn2`、`zstd`：libcurl-impersonate.dylib 运行期依赖

验证：

```bash
go version            # go1.21+
pkg-config --version  # 0.29+
ls /opt/homebrew/opt/libidn2/lib/libidn2.0.dylib
ls /opt/homebrew/opt/zstd/lib/libzstd.1.dylib
```

四条都要有正常输出。

---

## Step 2：下载并安装 libcurl-impersonate

```bash
cd /tmp
ARCH=$(uname -m)   # arm64 或 x86_64
VER=1.2.2

curl -L -O "https://github.com/lexiforest/curl-impersonate/releases/download/v${VER}/libcurl-impersonate-v${VER}.${ARCH}-macos.tar.gz"

# 包内是平铺的 dylib，不是 lib/ 结构，所以解压到中间目录再移动
mkdir -p libimp-extract
tar -xzf "libcurl-impersonate-v${VER}.${ARCH}-macos.tar.gz" -C libimp-extract

sudo mkdir -p /usr/local/lib
sudo mv libimp-extract/libcurl-impersonate.* /usr/local/lib/

# 验证：应该看到 4 个文件
ls /usr/local/lib/ | grep impersonate
# libcurl-impersonate.4.dylib
# libcurl-impersonate.a
# libcurl-impersonate.dylib  (软链)
# libcurl-impersonate.la
```

---

## Step 3：修 dylib 的 install_name（必须）

预编译包里 dylib 的 install_name 是 GitHub Actions 构建机的临时路径，直接用会报 "image not found"。修正：

```bash
sudo install_name_tool -id /usr/local/lib/libcurl-impersonate.4.dylib /usr/local/lib/libcurl-impersonate.4.dylib

# 验证：第二行应该是 /usr/local/lib/libcurl-impersonate.4.dylib，不是 /Users/runner/...
otool -L /usr/local/lib/libcurl-impersonate.dylib | head -3
```

---

## Step 4：解除 macOS 的 quarantine 标记

下载的 dylib 会被 Gatekeeper 标记隔离，运行时报 "code signature invalid"。清掉：

```bash
sudo xattr -dr com.apple.quarantine /usr/local/lib/libcurl-impersonate.*
```

（无输出即成功）

如果之后还报签名错，再补一道 ad-hoc 签名：

```bash
sudo codesign --force --sign - /usr/local/lib/libcurl-impersonate.4.dylib
sudo codesign --force --sign - /usr/local/lib/libcurl-impersonate.dylib
```

---

## Step 5：链接 curl 头文件

预编译包**不带头文件**，但 cgo 需要 `curl/curl.h`。用 Homebrew 的 curl 头文件：

```bash
sudo mkdir -p /usr/local/include
sudo ln -sf /opt/homebrew/opt/curl/include/curl /usr/local/include/curl

# Intel Mac 路径不同：
# sudo ln -sf /usr/local/opt/curl/include/curl /usr/local/include/curl

# 验证
ls /usr/local/include/curl/curl.h
```

---

## Step 6：手工创建 pkg-config 描述文件（关键）

`TeamMilestone/go-curl-impersonate` 内部用 `#cgo pkg-config: libcurl-impersonate` 来找库，但预编译包不带 `.pc` 文件。手工造一个：

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
pkg-config --libs libcurl-impersonate    # -L/usr/local/lib -lcurl-impersonate
pkg-config --cflags libcurl-impersonate  # -I/usr/local/include
```

---

## Step 7：配置环境变量

把下面这一段加到 `~/.zshrc`（Apple Silicon Mac 默认 shell 是 zsh）：

```bash
cat >> ~/.zshrc <<'EOF'

# libcurl-impersonate
export PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:${PKG_CONFIG_PATH}"
export DYLD_LIBRARY_PATH="/usr/local/lib:${DYLD_LIBRARY_PATH}"
export LIBRARY_PATH="/usr/local/lib:${LIBRARY_PATH}"
EOF

# 在新终端窗口生效，或：
source ~/.zshrc
```

> **不要重复加**：如果你之前手贱多 `cat >> ~/.zshrc` 了几次，打开 `~/.zshrc` 把重复段删掉，只留一份。

---

## Step 8：Go 端依赖

进项目目录：

```bash
go get github.com/TeamMilestone/go-curl-impersonate@latest
```

`lclient` 包：如果你已经把代码放进项目（如 `crawler-article/lclient/`），就在主代码里：

```go
import "crawler-article/lclient"   // 跟你的 module path 一致
```

---

## Step 9：一键自检脚本

存为 `lclient-doctor.sh`：

```bash
#!/usr/bin/env bash
ok()   { echo "  ✅ $*"; }
fail() { echo "  ❌ $*"; FAIL=1; }

echo "=== Go ==="
if go version >/dev/null 2>&1; then
  ok "$(go version)"
else
  fail "go not in PATH"
fi

echo
echo "=== dylib ==="
if [ -f /usr/local/lib/libcurl-impersonate.4.dylib ]; then
  ok "libcurl-impersonate.4.dylib exists"
  ID=$(otool -D /usr/local/lib/libcurl-impersonate.4.dylib | tail -1)
  if [ "$ID" = "/usr/local/lib/libcurl-impersonate.4.dylib" ]; then
    ok "install_name = $ID"
  else
    fail "install_name 错: $ID  (跑 install_name_tool 修)"
  fi
else
  fail "libcurl-impersonate.4.dylib 缺失"
fi

echo
echo "=== pkg-config ==="
if pkg-config --exists libcurl-impersonate; then
  ok "$(pkg-config --libs libcurl-impersonate)"
else
  fail "pkg-config 找不到 libcurl-impersonate (.pc 文件缺)"
fi

echo
echo "=== curl headers ==="
if [ -f /usr/local/include/curl/curl.h ]; then
  ok "curl/curl.h linked"
else
  fail "/usr/local/include/curl/curl.h 缺失"
fi

echo
echo "=== runtime deps ==="
for lib in libidn2.0 libzstd.1; do
  if [ -f "/opt/homebrew/opt/${lib%.*}/lib/${lib}.dylib" ] || \
     [ -f "/usr/local/opt/${lib%.*}/lib/${lib}.dylib" ]; then
    ok "$lib"
  else
    fail "$lib 缺失，跑 brew install ${lib%.*}"
  fi
done

echo
echo "=== env vars ==="
[ -n "$DYLD_LIBRARY_PATH" ] && ok "DYLD_LIBRARY_PATH=$DYLD_LIBRARY_PATH" || fail "DYLD_LIBRARY_PATH 未设"
[ -n "$PKG_CONFIG_PATH" ]   && ok "PKG_CONFIG_PATH=$PKG_CONFIG_PATH"     || fail "PKG_CONFIG_PATH 未设"

echo
if [ -z "$FAIL" ]; then
  echo "🎉 全部检查通过"
else
  echo "❗ 有未通过项，请按提示修复"
  exit 1
fi
```

```bash
chmod +x lclient-doctor.sh
./lclient-doctor.sh
```

---

## Step 10：最小可运行 demo

新建 `hello.go`：

```go
package main

import (
	"fmt"
	"crawler-article/lclient"  // 改成你的 module path
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

期望输出（具体 hash 值会随版本变化）：

```
JA3:    8d2e957d3bb44b8e2e4a9ad9d20c1cae
JA4:    t13d1517h2_8daaf6152771_b0da82dd1658
Akamai: 1:65536;3:1000;4:6291456;6:262144|15663105|0|m,p,a,s
UA:     Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36
```

只要这 4 个字段都不为空，环境就是好的。

---

## 故障对照表

| 报错 | 原因 | 修法 |
|---|---|---|
| `Package libcurl-impersonate was not found` | `.pc` 文件没建 / `PKG_CONFIG_PATH` 没设 | 回到 Step 6 + Step 7 |
| `'curl/curl.h' file not found` | 头文件没链 | 回到 Step 5 |
| `ld: library 'curl-impersonate' not found` | `LIBRARY_PATH` 没设 / dylib 不在 `/usr/local/lib` | 检查 Step 2 和 Step 7 |
| `dyld: Library not loaded: /Users/runner/...` | install_name 没修 | 回到 Step 3 |
| `code signature in <xxx> not valid` | quarantine 标记 / 未签名 | 回到 Step 4 |
| `image not found` (运行时) | `DYLD_LIBRARY_PATH` 没设 | 回到 Step 7 |
| `Symbol not found: _idn2_*` | libidn2 缺失 | `brew install libidn2` |
| `Symbol not found: _ZSTD_*` | zstd 缺失 | `brew install zstd` |
| 跑通了但 `ja3_hash` 跟 Go 默认一样 | profile 没生效 | 确认调用了 `WithImpersonate(...)` |

---

## 卸载

```bash
sudo rm -f /usr/local/lib/libcurl-impersonate.*
sudo rm -f /usr/local/lib/pkgconfig/libcurl-impersonate.pc
sudo rm -f /usr/local/include/curl    # 移除软链，不影响 brew 装的 curl
```

`~/.zshrc` 里手动删掉那段 export。