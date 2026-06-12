package lclient

// 常用浏览器 profile 常量。
//
// ⚠ 取值必须是当前链接的 libcurl-impersonate 真正编译进去的 target 名，否则
// 运行时 curl_easy_impersonate 会直接失败。下面这组是在 libcurl-impersonate
// 1.2.x 上逐个实测可用的目标（chrome133 / edge131 / safari18 / firefox117 等
// 在该版本里并不存在，故不暴露为常量）。换用别的库版本时请重新核对。
const (
	// Chrome
	ChromeLatest = "chrome131"
	Chrome131    = "chrome131"
	Chrome124    = "chrome124"
	Chrome120    = "chrome120"
	Chrome116    = "chrome116"
	Chrome110    = "chrome110"

	// Edge（该库仅编译了较旧的 edge99 / edge101）
	EdgeLatest = "edge101"
	Edge101    = "edge101"
	Edge99     = "edge99"

	// Safari
	SafariLatest  = "safari18_0"
	Safari18      = "safari18_0"
	Safari17      = "safari17_0"
	Safari17_2iOS = "safari17_2_ios"

	// Firefox
	FirefoxLatest = "firefox135"
	Firefox135    = "firefox135"
	Firefox133    = "firefox133"
)

// profileUA 根据 profile 返回匹配的 User-Agent。
// 风控站会同时校验 TLS 指纹 + UA，二者必须对齐，否则一眼识破。
// 这里维护一个保守的映射；如果你需要更精确的 UA，请自己 SetHeader 覆盖。
var profileUA = map[string]string{
	"chrome131": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"chrome124": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"chrome120": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"chrome116": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36",
	"chrome110": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/110.0.0.0 Safari/537.36",

	"edge101": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.4951.41 Safari/537.36 Edg/101.0.1210.32",
	"edge99":  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/99.0.4844.51 Safari/537.36 Edg/99.0.1150.36",

	"safari18_0":     "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
	"safari17_0":     "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
	"safari17_2_ios": "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1",

	"firefox135": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0",
	"firefox133": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
}

// UserAgentFor 返回 profile 对应的 UA；找不到时返回最新 Chrome UA。
func UserAgentFor(profile string) string {
	if ua, ok := profileUA[profile]; ok {
		return ua
	}
	return profileUA[ChromeLatest]
}
