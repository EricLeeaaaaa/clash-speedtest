package speedtester

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
	"gopkg.in/yaml.v3"
)

// 基本配置结构
type Config struct {
	ConfigPaths  string
	FilterRegex  string
	ServerURL    string
	DownloadSize int
	Timeout      time.Duration
	Concurrent   int
	NamePrefix   string
}

// 速度测试器
type SpeedTester struct {
	config      *Config
	clientCache sync.Map
}

// 创建新的测试器实例
func New(config *Config) *SpeedTester {
	// 设置默认值
	if config.Concurrent <= 0 {
		config.Concurrent = 1
	}
	if config.DownloadSize <= 0 {
		config.DownloadSize = 10 * 1024 * 1024
	}
	return &SpeedTester{config: config}
}

// 代理封装
type CProxy struct {
	constant.Proxy
	Config map[string]any
}

// 配置文件结构
type RawConfig struct {
	Providers map[string]map[string]any `yaml:"proxy-providers"`
	Proxies   []map[string]any          `yaml:"proxies"`
}

// 加载代理列表
func (st *SpeedTester) LoadProxies() (map[string]*CProxy, error) {
	allProxies := make(map[string]*CProxy)

	// 遍历所有配置路径
	for _, configPath := range strings.Split(st.config.ConfigPaths, ",") {
		var body []byte
		var err error

		// 读取配置内容
		if strings.HasPrefix(configPath, "http") {
			var resp *http.Response
			resp, err = http.Get(configPath)
			if err != nil {
				log.Warnln("failed to fetch config: %s", err)
				continue
			}
			body, err = io.ReadAll(resp.Body)
		} else {
			body, err = os.ReadFile(configPath)
		}
		if err != nil {
			log.Warnln("failed to read config: %s", err)
			continue
		}

		// 解析配置
		rawCfg := &RawConfig{Proxies: []map[string]any{}}
		if err := yaml.Unmarshal(body, rawCfg); err != nil {
			return nil, err
		}

		proxies := make(map[string]*CProxy)

		// 处理直接配置的代理
		for i, config := range rawCfg.Proxies {
			proxy, err := adapter.ParseProxy(config)
			if err != nil {
				return nil, fmt.Errorf("proxy %d: %w", i, err)
			}

			proxyName := proxy.Name()
			if st.config.NamePrefix != "" {
				proxyName = st.config.NamePrefix + proxyName
				config["name"] = proxyName
			}

			if _, exist := proxies[proxyName]; exist {
				return nil, fmt.Errorf("proxy %s is the duplicate name", proxyName)
			}

			proxies[proxyName] = &CProxy{
				Proxy:  proxy,
				Config: config,
			}
		}

		// 处理provider中的代理
		for name, config := range rawCfg.Providers {
			if name == provider.ReservedName {
				continue
			}
			pd, err := provider.ParseProxyProvider(name, config)
			if err != nil {
				continue
			}
			if err := pd.Initial(); err != nil {
				continue
			}

			for _, proxy := range pd.Proxies() {
				proxyName := proxy.Name()
				if st.config.NamePrefix != "" {
					proxyName = st.config.NamePrefix + proxyName
				}
				proxies[fmt.Sprintf("[%s] %s", name, proxyName)] = &CProxy{
					Proxy:  proxy,
					Config: nil,
				}
			}
		}

		// 筛选支持的代理类型
		for k, p := range proxies {
			switch p.Type() {
			case constant.Shadowsocks, constant.ShadowsocksR, constant.Snell, constant.Socks5, constant.Http,
				constant.Vmess, constant.Vless, constant.Trojan, constant.Hysteria, constant.Hysteria2,
				constant.WireGuard, constant.Tuic, constant.Ssh:
				if _, ok := allProxies[k]; !ok {
					allProxies[k] = p
				}
			}
		}
	}

	// 应用过滤器
	filterRegexp := regexp.MustCompile(st.config.FilterRegex)
	filteredProxies := make(map[string]*CProxy)
	for name, proxy := range allProxies {
		if filterRegexp.MatchString(name) {
			filteredProxies[name] = proxy
		}
	}

	return filteredProxies, nil
}

// 测试结果结构
type Result struct {
	ProxyName     string
	ProxyType     string
	ProxyConfig   map[string]any
	Latency       time.Duration
	DownloadSize  float64
	DownloadTime  time.Duration
	DownloadSpeed float64
}

// 格式化速度
func formatSpeed(bytesPerSecond float64) string {
	units := []string{"B/s", "KB/s", "MB/s", "GB/s"}
	unit := 0
	speed := bytesPerSecond
	for speed >= 1024 && unit < len(units)-1 {
		speed /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", speed, units[unit])
}

// 格式化方法
func (r *Result) FormatDownloadSpeed() string { return formatSpeed(r.DownloadSpeed) }
func (r *Result) FormatLatency() string {
	if r.Latency == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%dms", r.Latency.Milliseconds())
}

// 测试所有代理
func (st *SpeedTester) TestProxies(proxies map[string]*CProxy, fn func(result *Result)) {
	workers := make(chan struct{}, st.config.Concurrent)
	var wg sync.WaitGroup

	for name, proxy := range proxies {
		wg.Add(1)
		workers <- struct{}{}
		go func(name string, proxy *CProxy) {
			defer wg.Done()
			defer func() { <-workers }()

			// 为每个节点添加冷却时间
			time.Sleep(time.Second)
			fn(st.testProxy(name, proxy))
		}(name, proxy)
	}

	wg.Wait()
}

// 测试单个代理
func (st *SpeedTester) testProxy(name string, proxy *CProxy) *Result {
	result := &Result{
		ProxyName:   name,
		ProxyType:   proxy.Type().String(),
		ProxyConfig: proxy.Config,
	}

	client := st.getClient(proxy.Proxy)

	// 1. 延迟测试
	var latencies []time.Duration

	// 进行3次延迟测试
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), st.config.Timeout)
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/__down?bytes=0", st.config.ServerURL), nil)
		start := time.Now()
		resp, err := client.Do(req)
		cancel()

		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			latencies = append(latencies, time.Since(start))
		}

		// 测试间隔
		time.Sleep(300 * time.Millisecond)
	}

	// 计算延迟结果
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool {
			return latencies[i] < latencies[j]
		})
		// 取中位数作为最终延迟
		result.Latency = latencies[len(latencies)/2]
	}

	// 预热连接
	ctx, cancel := context.WithTimeout(context.Background(), st.config.Timeout)
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/__down?bytes=%d", st.config.ServerURL, 1024*512), nil)
	resp, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	cancel()
	time.Sleep(time.Second)

	// 2. 下载测试
	ctx, cancel = context.WithTimeout(context.Background(), st.config.Timeout)
	defer cancel()

	start := time.Now()
	req, _ = http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/__down?bytes=%d", st.config.ServerURL, st.config.DownloadSize), nil)
	resp, err = client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		downloadBytes, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		downloadTime := time.Since(start)

		result.DownloadSize = float64(downloadBytes)
		result.DownloadTime = downloadTime
		result.DownloadSpeed = float64(downloadBytes) / downloadTime.Seconds()
	}

	return result
}

// 获取HTTP客户端
func (st *SpeedTester) getClient(proxy constant.Proxy) *http.Client {
	key := proxy.Name()
	if client, ok := st.clientCache.Load(key); ok {
		return client.(*http.Client)
	}

	client := &http.Client{
		Timeout: st.config.Timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				var u16Port uint16
				if port, err := strconv.ParseUint(port, 10, 16); err == nil {
					u16Port = uint16(port)
				}
				return proxy.DialContext(ctx, &constant.Metadata{
					Host:    host,
					DstPort: u16Port,
				})
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
		},
	}

	st.clientCache.Store(key, client)
	return client
}
