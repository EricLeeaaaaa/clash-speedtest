package speedtester

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
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

type Config struct {
	ConfigPaths  string
	FilterRegex  string
	ServerURL    string
	DownloadSize int
	Timeout      time.Duration
	Concurrent   int
	NamePrefix   string
}

type SpeedTester struct {
	config      *Config
	clientCache sync.Map
}

func New(config *Config) *SpeedTester {
	if config.Concurrent <= 0 {
		config.Concurrent = 1
	}
	if config.DownloadSize <= 0 {
		config.DownloadSize = 10 * 1024 * 1024
	}
	return &SpeedTester{config: config}
}

type CProxy struct {
	constant.Proxy
	Config map[string]any
}

type RawConfig struct {
	Providers map[string]map[string]any `yaml:"proxy-providers"`
	Proxies   []map[string]any          `yaml:"proxies"`
}

func (st *SpeedTester) LoadProxies() (map[string]*CProxy, error) {
	proxies := make(map[string]*CProxy)
	filter := regexp.MustCompile(st.config.FilterRegex)

	for _, path := range strings.Split(st.config.ConfigPaths, ",") {
		var body []byte
		if strings.HasPrefix(path, "http") {
			resp, err := http.Get(path)
			if err != nil {
				log.Warnln("failed to fetch config: %s", err)
				continue
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		} else {
			body, _ = os.ReadFile(path)
		}

		rawCfg := &RawConfig{}
		if err := yaml.Unmarshal(body, rawCfg); err != nil {
			return nil, err
		}

		for _, cfg := range rawCfg.Proxies {
			if proxy, err := adapter.ParseProxy(cfg); err == nil && st.isSupported(proxy) {
				name := st.addPrefix(proxy.Name())
				if filter.MatchString(name) {
					proxies[name] = &CProxy{proxy, cfg}
				}
			}
		}

		for pname, cfg := range rawCfg.Providers {
			if pname == provider.ReservedName {
				continue
			}
			if pd, err := provider.ParseProxyProvider(pname, cfg); err == nil && pd.Initial() == nil {
				for _, proxy := range pd.Proxies() {
					if st.isSupported(proxy) {
						name := fmt.Sprintf("[%s] %s", pname, st.addPrefix(proxy.Name()))
						if filter.MatchString(name) {
							proxies[name] = &CProxy{proxy, nil}
						}
					}
				}
			}
		}
	}
	return proxies, nil
}

func (st *SpeedTester) isSupported(proxy constant.Proxy) bool {
	switch proxy.Type() {
	case constant.Shadowsocks, constant.ShadowsocksR, constant.Snell, constant.Socks5, constant.Http,
		constant.Vmess, constant.Vless, constant.Trojan, constant.Hysteria, constant.Hysteria2,
		constant.WireGuard, constant.Tuic, constant.Ssh:
		return true
	}
	return false
}

func (st *SpeedTester) addPrefix(name string) string {
	if st.config.NamePrefix != "" {
		return st.config.NamePrefix + name
	}
	return name
}

type Result struct {
	ProxyName     string
	ProxyType     string
	ProxyConfig   map[string]any
	Latency       time.Duration
	DownloadSpeed float64
}

func (r *Result) FormatLatency() string {
	if r.Latency == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%dms", r.Latency.Milliseconds())
}

func (r *Result) FormatDownloadSpeed() string {
	units := []string{"B/s", "KB/s", "MB/s", "GB/s"}
	unit := 0
	speed := r.DownloadSpeed
	for speed >= 1024 && unit < len(units)-1 {
		speed /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", speed, units[unit])
}

type downloadResult struct {
	size     int64
	duration time.Duration
}

func (st *SpeedTester) TestProxies(proxies map[string]*CProxy, fn func(result *Result)) {
	workers := make(chan struct{}, st.config.Concurrent)
	var wg sync.WaitGroup

	for name, proxy := range proxies {
		wg.Add(1)
		workers <- struct{}{}

		go func(name string, proxy *CProxy) {
			defer func() {
				if r := recover(); r != nil {
					log.Warnln("recovered from panic in test routine: %v", r)
				}
				wg.Done()
				<-workers
			}()

			result := st.testProxy(name, proxy)
			fn(result)
		}(name, proxy)
	}

	wg.Wait()
}

func (st *SpeedTester) testProxy(name string, proxy *CProxy) *Result {
	client := st.getClient(proxy.Proxy)

	// 建立初始连接
	resp, err := client.Get(fmt.Sprintf("%s/__down?bytes=%d", st.config.ServerURL, 32*1024))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	var dlResults []downloadResult
	resultChan := make(chan downloadResult, st.config.Concurrent)

	// 分块并发下载
	chunkSize := st.config.DownloadSize / st.config.Concurrent
	var wg sync.WaitGroup

	for i := 0; i < st.config.Concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result := st.downloadChunk(client, chunkSize); result.size > 0 {
				resultChan <- result
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果
	for result := range resultChan {
		dlResults = append(dlResults, result)
	}

	// 计算平均速度
	var totalBytes int64
	var totalTime time.Duration

	for _, result := range dlResults {
		totalBytes += result.size
		if result.duration > totalTime {
			totalTime = result.duration
		}
	}

	if totalTime > 0 {
		return &Result{
			ProxyName:     name,
			ProxyType:     proxy.Type().String(),
			ProxyConfig:   proxy.Config,
			DownloadSpeed: float64(totalBytes) / totalTime.Seconds(),
		}
	}

	return &Result{
		ProxyName:   name,
		ProxyType:   proxy.Type().String(),
		ProxyConfig: proxy.Config,
	}
}

func (st *SpeedTester) downloadChunk(client *http.Client, size int) downloadResult {
	start := time.Now()

	resp, err := client.Get(fmt.Sprintf("%s/__down?bytes=%d", st.config.ServerURL, size))
	if err != nil {
		return downloadResult{}
	}
	defer resp.Body.Close()

	buffer := make([]byte, 32*1024)
	var totalBytes int64

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			totalBytes += int64(n)
		}
		if err != nil {
			break
		}
	}

	return downloadResult{
		size:     totalBytes,
		duration: time.Since(start),
	}
}

func (st *SpeedTester) getClient(proxy constant.Proxy) *http.Client {
	if client, ok := st.clientCache.Load(proxy.Name()); ok {
		return client.(*http.Client)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, _ := net.SplitHostPort(addr)
			u16Port, _ := strconv.ParseUint(port, 10, 16)
			return proxy.DialContext(ctx, &constant.Metadata{Host: host, DstPort: uint16(u16Port)})
		},
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     false,
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   st.config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	st.clientCache.Store(proxy.Name(), client)
	return client
}
