package curlimpersonate

/*
#cgo pkg-config: libcurl-impersonate
#cgo darwin LDFLAGS: -L/opt/homebrew/lib -lidn2 -lzstd -licucore
#cgo linux LDFLAGS: -lidn2 -lzstd
#include <curl/curl.h>
#include <stdlib.h>
#include <string.h>
#include <arpa/inet.h>
#include <pthread.h>

// curl_easy_impersonate is not in standard headers, declare it
CURLcode curl_easy_impersonate(CURL *curl, const char *target, int default_headers);

// ===================== 跨句柄共享缓存（CURLSH）=====================
//
// 多个 easy 句柄共享同一份 DNS 缓存与 TLS 会话缓存：DNS 不必每个句柄各解析一次，
// TLS 会话票据可跨句柄复用（即便新建连接也能走 TLS resumption，握手更快）。即使
// 在"每请求新建一次性句柄"的无连接池模式下也有效——缓存活在 share 里，跨临时句柄存续。
//
// CURLSH 本身不是线程安全的：被多线程并发使用时，必须提供 LOCK/UNLOCK 回调，否则
// 共享缓存会被并发破坏。这里给每个 share 配一组按 curl_lock_data 分桶的互斥锁。

// curl_lock_data 取值上界（NONE..HSTS 目前 < 8），数组留足以容纳。
#define LC_LOCK_SLOTS 8

typedef struct {
    pthread_mutex_t locks[LC_LOCK_SLOTS];
} lc_locks_t;

typedef struct {
    CURLSH   *sh;
    lc_locks_t sl;
} lc_share_t;

static void lc_lock_cb(CURL *handle, curl_lock_data data, curl_lock_access access, void *userptr) {
    (void)handle; (void)access;
    lc_locks_t *sl = (lc_locks_t *)userptr;
    if (sl && (int)data >= 0 && (int)data < LC_LOCK_SLOTS) {
        pthread_mutex_lock(&sl->locks[data]);
    }
}

static void lc_unlock_cb(CURL *handle, curl_lock_data data, void *userptr) {
    (void)handle;
    lc_locks_t *sl = (lc_locks_t *)userptr;
    if (sl && (int)data >= 0 && (int)data < LC_LOCK_SLOTS) {
        pthread_mutex_unlock(&sl->locks[data]);
    }
}

// lc_share_new 创建一个共享 DNS + TLS 会话缓存的 share（含自带锁）。
static lc_share_t *lc_share_new() {
    lc_share_t *s = (lc_share_t *)calloc(1, sizeof(lc_share_t));
    if (!s) return NULL;
    for (int i = 0; i < LC_LOCK_SLOTS; i++) {
        pthread_mutex_init(&s->sl.locks[i], NULL);
    }
    s->sh = curl_share_init();
    if (!s->sh) {
        for (int i = 0; i < LC_LOCK_SLOTS; i++) pthread_mutex_destroy(&s->sl.locks[i]);
        free(s);
        return NULL;
    }
    curl_share_setopt(s->sh, CURLSHOPT_USERDATA, &s->sl);
    curl_share_setopt(s->sh, CURLSHOPT_LOCKFUNC, lc_lock_cb);
    curl_share_setopt(s->sh, CURLSHOPT_UNLOCKFUNC, lc_unlock_cb);
    curl_share_setopt(s->sh, CURLSHOPT_SHARE, CURL_LOCK_DATA_DNS);
    curl_share_setopt(s->sh, CURLSHOPT_SHARE, CURL_LOCK_DATA_SSL_SESSION);
    return s;
}

// lc_share_free 释放 share。必须在所有使用它的 easy 句柄都已 cleanup 之后调用。
static void lc_share_free(lc_share_t *s) {
    if (!s) return;
    if (s->sh) curl_share_cleanup(s->sh);
    for (int i = 0; i < LC_LOCK_SLOTS; i++) pthread_mutex_destroy(&s->sl.locks[i]);
    free(s);
}

// is_blocked_ip 判断一个已解析出的 IP 是否落在"不该被爬虫访问"的私有/特殊网段，
// 用于 SSRF 防护。覆盖 IPv4/IPv6 的环回、私网、链路本地、CGNAT、唯一本地地址，
// 以及 IPv4-mapped IPv6（::ffff:a.b.c.d，回落到 v4 规则判断，防绕过）。
static int is_blocked_ip(const char *ip) {
    if (!ip || !*ip) return 0;

    struct in_addr v4;
    if (inet_pton(AF_INET, ip, &v4) == 1) {
        unsigned long a = ntohl(v4.s_addr);
        unsigned char b0 = (a >> 24) & 0xff;
        unsigned char b1 = (a >> 16) & 0xff;
        if (b0 == 0) return 1;                              // 0.0.0.0/8
        if (b0 == 10) return 1;                             // 10/8 私网
        if (b0 == 127) return 1;                            // 127/8 环回
        if (b0 == 169 && b1 == 254) return 1;               // 169.254/16 链路本地（含云元数据）
        if (b0 == 172 && b1 >= 16 && b1 <= 31) return 1;    // 172.16/12 私网
        if (b0 == 192 && b1 == 168) return 1;               // 192.168/16 私网
        if (b0 == 100 && b1 >= 64 && b1 <= 127) return 1;   // 100.64/10 CGNAT
        return 0;
    }

    struct in6_addr v6;
    if (inet_pton(AF_INET6, ip, &v6) == 1) {
        static const unsigned char loop[16] = {0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1};
        static const unsigned char any[16]  = {0};
        if (memcmp(v6.s6_addr, loop, 16) == 0) return 1;    // ::1 环回
        if (memcmp(v6.s6_addr, any, 16) == 0) return 1;     // :: 未指定
        unsigned char c0 = v6.s6_addr[0], c1 = v6.s6_addr[1];
        if ((c0 & 0xfe) == 0xfc) return 1;                  // fc00::/7 唯一本地
        if (c0 == 0xfe && (c1 & 0xc0) == 0x80) return 1;    // fe80::/10 链路本地
        // ::ffff:0:0/96 IPv4-mapped：解出内嵌 v4 再按 v4 规则判，防止映射绕过。
        static const unsigned char mapped[12] = {0,0,0,0,0,0,0,0,0,0,0xff,0xff};
        if (memcmp(v6.s6_addr, mapped, 12) == 0) {
            char buf[INET_ADDRSTRLEN];
            if (inet_ntop(AF_INET, &v6.s6_addr[12], buf, sizeof(buf)))
                return is_blocked_ip(buf);
        }
        return 0;
    }
    return 0;
}

// prereq_cb 在连接建立后、请求发出前触发。clientp 指向"是否拦截私网"的标志；
// 命中私网 IP 时返回 ABORT，使 curl_easy_perform 以 CURLE_ABORTED_BY_CALLBACK 失败。
// 在这里拦截（而非请求前预解析）可同时挡住 DNS rebinding 与重定向到私网的情况。
static int prereq_cb(void *clientp, char *conn_primary_ip, char *conn_local_ip,
                     int conn_primary_port, int conn_local_port) {
    (void)conn_local_ip; (void)conn_primary_port; (void)conn_local_port;
    int block = clientp ? *(int *)clientp : 0;
    if (block && is_blocked_ip(conn_primary_ip)) {
        return CURL_PREREQFUNC_ABORT;
    }
    return CURL_PREREQFUNC_OK;
}

// xferinfo_cb 是进度回调，curl 在连接 / 传输期间周期性调用它。clientp 指向一个
// "取消标志"：一旦被置为非 0（由 Go 侧监听 context 取消时写入），返回非 0 让
// curl 立刻中止本次传输（perform 返回 CURLE_ABORTED_BY_CALLBACK）。
// 这样 context 取消 / 超时能真正打断"在途"的同步 cgo 请求，而不必干等 curl 超时。
static int xferinfo_cb(void *clientp, curl_off_t dltotal, curl_off_t dlnow,
                       curl_off_t ultotal, curl_off_t ulnow) {
    (void)dltotal; (void)dlnow; (void)ultotal; (void)ulnow;
    if (clientp && *(int *)clientp) return 1;
    return 0;
}

// Write / Header callback 共用的累积缓冲。
struct MemoryStruct {
    char *memory;
    size_t size;     // 已用字节
    size_t capacity; // 已分配字节（含结尾 NUL 余量）
    size_t limit;    // 响应体上限（字节），0 = 不限制
    int    overflow; // 超过 limit 时置 1，用于区分"超限中止"与真正的传输错误
};

// mem_append 把 len 字节追加进缓冲，按几何倍增扩容（容量不足才 realloc）。
// 相比"每个数据块都 realloc"，把扩容次数从 O(n) 降到 O(log n)，规避大响应下的
// O(n²) 拷贝与高并发时的分配器压力。返回 0 表示 OOM。
static int mem_append(struct MemoryStruct *mem, const void *data, size_t len) {
    size_t need = mem->size + len + 1; // +1 给结尾 NUL
    if (need > mem->capacity) {
        size_t newcap = mem->capacity ? mem->capacity : 1024;
        while (newcap < need) newcap *= 2;
        char *ptr = realloc(mem->memory, newcap);
        if (!ptr) return 0;
        mem->memory = ptr;
        mem->capacity = newcap;
    }
    memcpy(mem->memory + mem->size, data, len);
    mem->size += len;
    mem->memory[mem->size] = 0;
    return 1;
}

static size_t WriteMemoryCallback(void *contents, size_t size, size_t nmemb, void *userp) {
    size_t realsize = size * nmemb;
    struct MemoryStruct *mem = (struct MemoryStruct *)userp;
    // 响应体大小上限：防止解压炸弹 / 误抓超大资源耗尽内存。超限即中止传输
    // （返回 0 会让 curl_easy_perform 返回 CURLE_WRITE_ERROR）。
    if (mem->limit > 0 && mem->size + realsize > mem->limit) {
        mem->overflow = 1;
        return 0;
    }
    if (!mem_append(mem, contents, realsize)) return 0;
    return realsize;
}

static size_t HeaderCallback(void *contents, size_t size, size_t nmemb, void *userp) {
    size_t realsize = size * nmemb;
    if (!mem_append((struct MemoryStruct *)userp, contents, realsize)) return 0;
    return realsize;
}

typedef struct {
    long status_code;
    char *body;
    size_t body_size;
    char *headers;
    size_t headers_size;
    char *error;
} CurlResponse;

// curl_do_request 执行一次请求。
//
// 相比早期版本的改动：
//   - method        : 显式 HTTP 方法（GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS），
//                     不再让所有非 GET 请求塌缩成 GET/POST。
//   - header_keys/vals 已是有序数组，按数组顺序 curl_slist_append，保留 header 顺序。
//   - post_data + post_size : body 以「指针 + 长度」传入，二进制 / 含 NUL 字节
//                     的 body 不再被 strlen 截断。
//   - verify_tls    : 1 时校验目标证书（VERIFYPEER+VERIFYHOST），0 时跳过。
//   - 自定义 header 拼接改为按需 malloc，超长 header（如大 Cookie）不再被 4096 截断。
// do_on_handle 在「已就绪的句柄」上执行一次请求：设置全部 option、发起请求、
// 收集响应。它既不 init 也不 cleanup 句柄——句柄的生命周期由调用方掌管，这正是
// 连接复用的关键（同一句柄跨请求复用时，libcurl 的连接缓存 / TLS 会话缓存 / DNS
// 缓存得以保留，省掉重复的 TCP + TLS 握手）。
static CurlResponse do_on_handle(CURL *curl, const char *url, const char *proxy, const char *proxy_userpwd,
                              const char *impersonate_target, const char *method,
                              const char **header_keys, const char **header_vals, int header_count,
                              const char *post_data, long post_size,
                              int follow_redirects, int timeout_sec, int verify_tls,
                              long max_body_size, int block_private_ips, void *share,
                              void *cancel_flag) {
    CurlResponse resp = {0, NULL, 0, NULL, 0, NULL};

    // 字段顺序：memory, size, capacity, limit, overflow。malloc(1) → 初始容量 1。
    struct MemoryStruct body_chunk = {malloc(1), 0, 1, 0, 0};
    struct MemoryStruct header_chunk = {malloc(1), 0, 1, 0, 0};
    if (max_body_size > 0) {
        body_chunk.limit = (size_t)max_body_size;
    }

    // Impersonate（必须最先调用：它会设一批默认 option，之后我们再覆盖）
    CURLcode imp_res = curl_easy_impersonate(curl, impersonate_target, 1);
    if (imp_res != CURLE_OK) {
        resp.error = strdup("curl_easy_impersonate failed");
        free(body_chunk.memory);
        free(header_chunk.memory);
        return resp;
    }

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, WriteMemoryCallback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body_chunk);
    curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, HeaderCallback);
    curl_easy_setopt(curl, CURLOPT_HEADERDATA, &header_chunk);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, (long)timeout_sec);
    // 空串 = 启用 libcurl 编译进来的全部解压算法（含本库链接的 zstd）。
    // 这样即便我们在自定义 header 里把 Accept-Encoding 写成 "gzip, deflate, br, zstd"
    // （与真实 Chrome 一致），返回的 zstd / br 响应也能被正确解压，不会把压缩字节
    // 原样交给调用方。实际发送的 Accept-Encoding 仍以自定义 header 为准。
    curl_easy_setopt(curl, CURLOPT_ACCEPT_ENCODING, "");

    // TLS 校验：默认开启，调用方显式要求时才跳过。
    if (verify_tls) {
        curl_easy_setopt(curl, CURLOPT_SSL_VERIFYPEER, 1L);
        curl_easy_setopt(curl, CURLOPT_SSL_VERIFYHOST, 2L);
    } else {
        curl_easy_setopt(curl, CURLOPT_SSL_VERIFYPEER, 0L);
        curl_easy_setopt(curl, CURLOPT_SSL_VERIFYHOST, 0L);
    }
    // 代理证书不校验：隧道代理常用自签证书，且目标站点 HTTPS 在 CONNECT 隧道内
    // 仍是端到端 TLS，受上面的目标校验保护。这样 https:// 代理可直接使用。
    curl_easy_setopt(curl, CURLOPT_PROXY_SSL_VERIFYPEER, 0L);
    curl_easy_setopt(curl, CURLOPT_PROXY_SSL_VERIFYHOST, 0L);

    // 仅允许 http/https：默认情况下 libcurl 还支持 file/scp/gopher/dict 等协议，
    // 否则 Get("file:///etc/passwd") 或被重定向到 file:// 就能读本地文件 / 触发 SSRF。
    // 主请求与重定向都限制掉。
    curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "http,https");
    curl_easy_setopt(curl, CURLOPT_REDIR_PROTOCOLS_STR, "http,https");

    // 连接保活：让池中复用的连接更不易被中间设备静默断开。
    curl_easy_setopt(curl, CURLOPT_TCP_KEEPALIVE, 1L);

    // 跨句柄共享 DNS / TLS 会话缓存（reset 会清掉，故每次都要重设）。
    if (share) {
        curl_easy_setopt(curl, CURLOPT_SHARE, ((lc_share_t *)share)->sh);
    }

    // context 取消支持：启用进度回调，让 Go 侧置位取消标志后能中止在途传输。
    if (cancel_flag) {
        curl_easy_setopt(curl, CURLOPT_XFERINFOFUNCTION, xferinfo_cb);
        curl_easy_setopt(curl, CURLOPT_XFERINFODATA, cancel_flag);
        curl_easy_setopt(curl, CURLOPT_NOPROGRESS, 0L);
    }

    // SSRF 防护：拦截解析到私网 / 环回 / 链路本地的地址（在连接后、发请求前判定，
    // 可同时挡住 DNS rebinding 与重定向到内网）。block_flag 的地址在本函数栈上，
    // 在 curl_easy_perform 同步执行期间始终有效。
    int block_flag = block_private_ips;
    if (block_private_ips) {
        curl_easy_setopt(curl, CURLOPT_PREREQFUNCTION, prereq_cb);
        curl_easy_setopt(curl, CURLOPT_PREREQDATA, &block_flag);
    }

    if (!follow_redirects) {
        curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 0L);
    } else {
        curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
    }

    if (proxy && strlen(proxy) > 0) {
        curl_easy_setopt(curl, CURLOPT_PROXY, proxy);
        if (proxy_userpwd && strlen(proxy_userpwd) > 0) {
            curl_easy_setopt(curl, CURLOPT_PROXYUSERPWD, proxy_userpwd);
        }
    }

    // Custom headers（按数组顺序，保留 header 顺序；按需分配避免截断）
    struct curl_slist *headers = NULL;
    for (int i = 0; i < header_count; i++) {
        size_t klen = strlen(header_keys[i]);
        size_t vlen = strlen(header_vals[i]);
        // need = key + ": " + val + 结尾 NUL
        size_t need = klen + 2 + vlen + 1;
        char *buf = (char *)malloc(need);
        if (!buf) continue;
        snprintf(buf, need, "%s: %s", header_keys[i], header_vals[i]);
        headers = curl_slist_append(headers, buf);
        free(buf); // curl_slist_append 内部复制了字符串
    }
    if (headers) {
        curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    }

    // HTTP 方法
    if (method && strlen(method) > 0) {
        if (strcmp(method, "HEAD") == 0) {
            curl_easy_setopt(curl, CURLOPT_NOBODY, 1L);
        } else if (strcmp(method, "GET") != 0) {
            // POST/PUT/DELETE/PATCH/OPTIONS 等显式方法
            curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, method);
        }
    }

    // Body：以指针 + 长度传入，二进制安全（COPYPOSTFIELDS 复制 post_size 字节）。
    if (post_size > 0 && post_data) {
        curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, post_size);
        curl_easy_setopt(curl, CURLOPT_COPYPOSTFIELDS, post_data);
    }

    CURLcode res = curl_easy_perform(curl);

    if (res != CURLE_OK) {
        if (body_chunk.overflow) {
            resp.error = strdup("response body exceeds max size limit");
        } else if (res == CURLE_ABORTED_BY_CALLBACK && cancel_flag && *(int *)cancel_flag) {
            resp.error = strdup("request canceled by context");
        } else if (res == CURLE_ABORTED_BY_CALLBACK && block_private_ips) {
            resp.error = strdup("blocked private or loopback address (SSRF protection)");
        } else {
            resp.error = strdup(curl_easy_strerror(res));
        }
    } else {
        curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &resp.status_code);
        resp.body = body_chunk.memory;
        resp.body_size = body_chunk.size;
        resp.headers = header_chunk.memory;
        resp.headers_size = header_chunk.size;
        body_chunk.memory = NULL;
        header_chunk.memory = NULL;
    }

    if (headers) curl_slist_free_all(headers);
    if (body_chunk.memory) free(body_chunk.memory);
    if (header_chunk.memory) free(header_chunk.memory);

    return resp;
}

// curl_do_request 一次性句柄：init → 执行 → cleanup（无连接复用，向后兼容）。
CurlResponse curl_do_request(const char *url, const char *proxy, const char *proxy_userpwd,
                              const char *impersonate_target, const char *method,
                              const char **header_keys, const char **header_vals, int header_count,
                              const char *post_data, long post_size,
                              int follow_redirects, int timeout_sec, int verify_tls,
                              long max_body_size, int block_private_ips, void *share,
                              void *cancel_flag) {
    CURL *curl = curl_easy_init();
    if (!curl) {
        CurlResponse resp = {0, NULL, 0, NULL, 0, NULL};
        resp.error = strdup("curl_easy_init failed");
        return resp;
    }
    CurlResponse resp = do_on_handle(curl, url, proxy, proxy_userpwd, impersonate_target, method,
                                     header_keys, header_vals, header_count, post_data, post_size,
                                     follow_redirects, timeout_sec, verify_tls, max_body_size, block_private_ips, share,
                                     cancel_flag);
    curl_easy_cleanup(curl);
    return resp;
}

// curl_do_request_reuse 复用句柄：reset（保留连接 / TLS 会话 / DNS 缓存）→ 执行。
// 不 cleanup，句柄交还调用方继续复用。注意：单个句柄绝不能被多个线程并发使用。
CurlResponse curl_do_request_reuse(CURL *curl, const char *url, const char *proxy, const char *proxy_userpwd,
                              const char *impersonate_target, const char *method,
                              const char **header_keys, const char **header_vals, int header_count,
                              const char *post_data, long post_size,
                              int follow_redirects, int timeout_sec, int verify_tls,
                              long max_body_size, int block_private_ips, void *share,
                              void *cancel_flag) {
    if (!curl) {
        CurlResponse resp = {0, NULL, 0, NULL, 0, NULL};
        resp.error = strdup("nil curl handle");
        return resp;
    }
    // reset 只清 option，不动 live connections / Session ID cache / DNS cache，
    // 因此连接得以复用；随后 do_on_handle 会重新设置本次请求所需的全部 option。
    curl_easy_reset(curl);
    return do_on_handle(curl, url, proxy, proxy_userpwd, impersonate_target, method,
                        header_keys, header_vals, header_count, post_data, post_size,
                        follow_redirects, timeout_sec, verify_tls, max_body_size, block_private_ips, share,
                        cancel_flag);
}

void free_response(CurlResponse *resp) {
    if (resp->body) { free(resp->body); resp->body = NULL; }
    if (resp->headers) { free(resp->headers); resp->headers = NULL; }
    if (resp->error) { free(resp->error); resp->error = NULL; }
}
*/
import "C"
import (
	"context"
	"fmt"
	"strings"
	"unsafe"
)

func init() {
	C.curl_global_init(C.CURL_GLOBAL_DEFAULT)
}

type Response struct {
	StatusCode int
	Body       []byte
	Headers    string
}

// Request 是一次请求的完整描述。HeaderKeys / HeaderVals 必须等长且一一对应，
// 顺序即为实际发送顺序（header 顺序是反爬指纹的一部分）。
type Request struct {
	URL             string
	Proxy           string
	Impersonate     string
	Method          string
	HeaderKeys      []string
	HeaderVals      []string
	Body            []byte
	FollowRedirects bool
	TimeoutSec      int
	VerifyTLS       bool   // true 时校验目标 TLS 证书
	MaxBodyBytes    int64  // 响应体上限（字节），0 = 不限制
	BlockPrivateIPs bool   // true 时拦截解析到私网/环回/链路本地的地址（SSRF 防护）
	Share           *Share // 非 nil 时复用其 DNS / TLS 会话缓存
	// Ctx 非 nil 且可取消时，取消 / 超时会中止在途传输（而非干等 curl 超时）。
	Ctx context.Context
}

// Share 是一组 easy 句柄共享的 DNS / TLS 会话缓存（线程安全，内部自带锁）。
// 用完须 Close；Close 必须在所有用到它的请求 / 句柄都结束之后调用。
type Share struct {
	p unsafe.Pointer // *C.lc_share_t
}

// NewShare 创建一个共享缓存。
func NewShare() (*Share, error) {
	p := C.lc_share_new()
	if p == nil {
		return nil, fmt.Errorf("curl_share_init failed")
	}
	return &Share{p: unsafe.Pointer(p)}, nil
}

// Close 释放共享缓存。须在没有任何在途请求、且相关句柄均已 Close 后调用。幂等。
func (s *Share) Close() {
	if s != nil && s.p != nil {
		C.lc_share_free((*C.lc_share_t)(s.p))
		s.p = nil
	}
}

// Do 按 Request 执行请求（一次性句柄，无连接复用）。保留 header 顺序、支持二进制
// body、显式方法与 TLS 校验开关。需要连接复用请改用 Handle。
func Do(req Request) (*Response, error) {
	return marshalAndPerform(nil, req)
}

// Handle 封装一个可复用的 curl easy 句柄。复用句柄能保留连接 / TLS 会话 / DNS
// 缓存，从而跳过重复握手。
//
// ⚠ 并发：单个 Handle 不是线程安全的，绝不能被多个 goroutine 同时调用 Do。
// 典型用法是放进一个池子里，每次借出给一个 goroutine 独占使用、用完归还。
type Handle struct {
	// curl.h 把 CURL 定义为 void（不透明），cgo 里对应 unsafe.Pointer。
	h unsafe.Pointer
}

// NewHandle 创建一个可复用句柄。用完必须 Close 释放底层 C 资源。
func NewHandle() (*Handle, error) {
	h := C.curl_easy_init()
	if h == nil {
		return nil, fmt.Errorf("curl_easy_init failed")
	}
	return &Handle{h: h}, nil
}

// Do 在该句柄上执行一次请求（复用其连接缓存）。非并发安全。
func (hd *Handle) Do(req Request) (*Response, error) {
	if hd == nil || hd.h == nil {
		return nil, fmt.Errorf("use of closed curl handle")
	}
	return marshalAndPerform(hd.h, req)
}

// Close 释放底层 C 句柄（含其持有的所有连接）。幂等。
func (hd *Handle) Close() {
	if hd != nil && hd.h != nil {
		C.curl_easy_cleanup(hd.h)
		hd.h = nil
	}
}

// marshalAndPerform 把 Request 编排成 C 调用。handle 为 nil 时走一次性句柄
// （curl_do_request），非 nil 时复用该句柄（curl_do_request_reuse）。两条路径
// 共用同一套 CString 编组逻辑，避免重复。
func marshalAndPerform(handle unsafe.Pointer, req Request) (*Response, error) {
	proxyAddr, proxyAuth := parseProxyURL(req.Proxy)

	cURL := C.CString(req.URL)
	defer C.free(unsafe.Pointer(cURL))

	cProxy := C.CString(proxyAddr)
	defer C.free(unsafe.Pointer(cProxy))

	cProxyAuth := C.CString(proxyAuth)
	defer C.free(unsafe.Pointer(cProxyAuth))

	cImpersonate := C.CString(req.Impersonate)
	defer C.free(unsafe.Pointer(cImpersonate))

	cMethod := C.CString(req.Method)
	defer C.free(unsafe.Pointer(cMethod))

	if len(req.HeaderKeys) != len(req.HeaderVals) {
		return nil, fmt.Errorf("header keys/vals length mismatch: %d vs %d", len(req.HeaderKeys), len(req.HeaderVals))
	}
	headerCount := len(req.HeaderKeys)
	cKeys := make([]*C.char, headerCount)
	cVals := make([]*C.char, headerCount)
	for i := 0; i < headerCount; i++ {
		cKeys[i] = C.CString(req.HeaderKeys[i])
		cVals[i] = C.CString(req.HeaderVals[i])
	}
	defer func() {
		for j := 0; j < headerCount; j++ {
			C.free(unsafe.Pointer(cKeys[j]))
			C.free(unsafe.Pointer(cVals[j]))
		}
	}()

	var keysPtr, valsPtr **C.char
	if headerCount > 0 {
		keysPtr = &cKeys[0]
		valsPtr = &cVals[0]
	}

	// body 以指针 + 长度传入，二进制安全。
	var bodyPtr *C.char
	if len(req.Body) > 0 {
		cBody := C.CBytes(req.Body)
		defer C.free(cBody)
		bodyPtr = (*C.char)(cBody)
	}

	follow := C.int(0)
	if req.FollowRedirects {
		follow = 1
	}
	verify := C.int(0)
	if req.VerifyTLS {
		verify = 1
	}
	blockIPs := C.int(0)
	if req.BlockPrivateIPs {
		blockIPs = 1
	}
	var sharePtr unsafe.Pointer
	if req.Share != nil {
		sharePtr = req.Share.p
	}

	// context 取消桥接：当 ctx 可取消时，分配一个 C 端取消标志，并起一个 watcher
	// goroutine——ctx 取消即把标志置 1，curl 的进度回调随之中止在途传输。请求返回前
	// 必须确保 watcher 已退出，再释放标志，避免写已释放内存。
	var cancelPtr unsafe.Pointer
	if req.Ctx != nil && req.Ctx.Done() != nil {
		cancelPtr = C.calloc(1, C.size_t(unsafe.Sizeof(C.int(0))))
		stop := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			select {
			case <-req.Ctx.Done():
				*(*C.int)(cancelPtr) = 1
			case <-stop:
			}
		}()
		defer func() {
			close(stop)
			<-finished
			C.free(cancelPtr)
		}()
	}

	var resp C.CurlResponse
	if handle == nil {
		resp = C.curl_do_request(cURL, cProxy, cProxyAuth, cImpersonate, cMethod,
			keysPtr, valsPtr, C.int(headerCount),
			bodyPtr, C.long(len(req.Body)),
			follow, C.int(req.TimeoutSec), verify,
			C.long(req.MaxBodyBytes), blockIPs, sharePtr, cancelPtr)
	} else {
		resp = C.curl_do_request_reuse(handle, cURL, cProxy, cProxyAuth, cImpersonate, cMethod,
			keysPtr, valsPtr, C.int(headerCount),
			bodyPtr, C.long(len(req.Body)),
			follow, C.int(req.TimeoutSec), verify,
			C.long(req.MaxBodyBytes), blockIPs, sharePtr, cancelPtr)
	}
	defer C.free_response(&resp)

	if resp.error != nil {
		return nil, fmt.Errorf("%s", C.GoString(resp.error))
	}

	return &Response{
		StatusCode: int(resp.status_code),
		Body:       C.GoBytes(unsafe.Pointer(resp.body), C.int(resp.body_size)),
		Headers:    C.GoStringN(resp.headers, C.int(resp.headers_size)),
	}, nil
}

// DoRequest 保留旧签名以向后兼容（map 顺序不可控、body 经 strlen、不校验 TLS）。
// 新代码请改用 Do(Request{...})。
func DoRequest(url, proxy, impersonate string, headers map[string]string, postData string, followRedirects bool, timeoutSec int) (*Response, error) {
	keys := make([]string, 0, len(headers))
	vals := make([]string, 0, len(headers))
	for k, v := range headers {
		keys = append(keys, k)
		vals = append(vals, v)
	}
	return Do(Request{
		URL:             url,
		Proxy:           proxy,
		Impersonate:     impersonate,
		Method:          "", // 未知方法：有 body 走 POST，否则 GET（旧行为）
		HeaderKeys:      keys,
		HeaderVals:      vals,
		Body:            []byte(postData),
		FollowRedirects: followRedirects,
		TimeoutSec:      timeoutSec,
		VerifyTLS:       false,
	})
}

func parseProxyURL(proxy string) (string, string) {
	if proxy == "" {
		return "", ""
	}
	rest := proxy
	scheme := ""
	if idx := strings.Index(proxy, "://"); idx >= 0 {
		scheme = proxy[:idx+3]
		rest = proxy[idx+3:]
	}
	if idx := strings.LastIndex(rest, "@"); idx >= 0 {
		auth := rest[:idx]
		host := rest[idx+1:]
		return scheme + host, auth
	}
	return proxy, ""
}

func ExtractCookies(headers string) map[string]string {
	cookies := make(map[string]string)
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "set-cookie:") {
			parts := strings.SplitN(line[len("set-cookie:"):], ";", 2)
			kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
			if len(kv) == 2 {
				cookies[kv[0]] = kv[1]
			}
		}
	}
	return cookies
}
