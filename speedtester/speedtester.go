package speedtester

import (
	"context"
	"fmt"
	"io"
	"math"
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
	UploadSize   int
	Timeout      time.Duration
	Concurrent   int
	NamePrefix   string // 新增: 节点名称前缀
}

type SpeedTester struct {
	config *Config
}

func New(config *Config) *SpeedTester {
	if config.Concurrent <= 0 {
		config.Concurrent = 1
	}
	if config.DownloadSize <= 0 {
		config.DownloadSize = 100 * 1024 * 1024
	}
	if config.UploadSize <= 0 {
		config.UploadSize = 10 * 1024 * 1024
	}
	return &SpeedTester{
		config: config,
	}
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
	allProxies := make(map[string]*CProxy)

	for _, configPath := range strings.Split(st.config.ConfigPaths, ",") {
		var body []byte
		var err error
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

		rawCfg := &RawConfig{
			Proxies: []map[string]any{},
		}
		if err := yaml.Unmarshal(body, rawCfg); err != nil {
			return nil, err
		}
		proxies := make(map[string]*CProxy)
		proxiesConfig := rawCfg.Proxies
		providersConfig := rawCfg.Providers

		for i, config := range proxiesConfig {
			proxy, err := adapter.ParseProxy(config)
			if err != nil {
				return nil, fmt.Errorf("proxy %d: %w", i, err)
			}

			proxyName := proxy.Name()
			if st.config.NamePrefix != "" {
				proxyName = st.config.NamePrefix + proxyName
				config["name"] = proxyName // 更新配置中的节点名称
			}

			if _, exist := proxies[proxyName]; exist {
				return nil, fmt.Errorf("proxy %s is the duplicate name", proxyName)
			}

			// 使用更新后的配置重新解析代理
			proxy, err = adapter.ParseProxy(config)
			if err != nil {
				return nil, fmt.Errorf("proxy %s: %w", proxyName, err)
			}

			proxies[proxyName] = &CProxy{
				Proxy:  proxy,
				Config: config,
			}
		}

		for name, config := range providersConfig {
			if name == provider.ReservedName {
				return nil, fmt.Errorf("can not defined a provider called `%s`", provider.ReservedName)
			}
			pd, err := provider.ParseProxyProvider(name, config)
			if err != nil {
				return nil, fmt.Errorf("parse proxy provider %s error: %w", name, err)
			}
			if err := pd.Initial(); err != nil {
				return nil, fmt.Errorf("initial proxy provider %s error: %w", pd.Name(), err)
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

		for k, p := range proxies {
			switch p.Type() {
			case constant.Shadowsocks, constant.ShadowsocksR, constant.Snell, constant.Socks5, constant.Http,
				constant.Vmess, constant.Vless, constant.Trojan, constant.Hysteria, constant.Hysteria2,
				constant.WireGuard, constant.Tuic, constant.Ssh:
			default:
				continue
			}
			if _, ok := allProxies[k]; !ok {
				allProxies[k] = p
			}
		}
	}

	filterRegexp := regexp.MustCompile(st.config.FilterRegex)
	filteredProxies := make(map[string]*CProxy)
	for name := range allProxies {
		if filterRegexp.MatchString(name) {
			filteredProxies[name] = allProxies[name]
		}
	}
	return filteredProxies, nil
}

func (st *SpeedTester) TestProxies(proxies map[string]*CProxy, fn func(result *Result)) {
	// 创建工作池和结果通道
	workers := make(chan struct{}, 10) // 限制最大并发数为10
	var wg sync.WaitGroup

	for name, proxy := range proxies {
		wg.Add(1)
		workers <- struct{}{} // 获取工作槽
		go func(name string, proxy *CProxy) {
			defer wg.Done()
			defer func() { <-workers }() // 释放工作槽
			fn(st.testProxy(name, proxy))
		}(name, proxy)
	}

	wg.Wait()
}

type Result struct {
	ProxyName     string         `json:"proxy_name"`
	ProxyType     string         `json:"proxy_type"`
	ProxyConfig   map[string]any `json:"proxy_config"`
	Latency       time.Duration  `json:"latency"`
	Jitter        time.Duration  `json:"jitter"`
	PacketLoss    float64        `json:"packet_loss"`
	DownloadSize  float64        `json:"download_size"`
	DownloadTime  time.Duration  `json:"download_time"`
	DownloadSpeed float64        `json:"download_speed"`
	UploadSize    float64        `json:"upload_size"`
	UploadTime    time.Duration  `json:"upload_time"`
	UploadSpeed   float64        `json:"upload_speed"`
}

func (r *Result) FormatDownloadSpeed() string {
	return formatSpeed(r.DownloadSpeed)
}

func (r *Result) FormatLatency() string {
	if r.Latency == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%dms", r.Latency.Milliseconds())
}

func (r *Result) FormatJitter() string {
	if r.Jitter == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%dms", r.Jitter.Milliseconds())
}

func (r *Result) FormatPacketLoss() string {
	return fmt.Sprintf("%.1f%%", r.PacketLoss)
}

func (r *Result) FormatUploadSpeed() string {
	return formatSpeed(r.UploadSpeed)
}

func formatSpeed(bytesPerSecond float64) string {
	units := []string{"B/s", "KB/s", "MB/s", "GB/s", "TB/s"}
	unit := 0
	speed := bytesPerSecond
	for speed >= 1024 && unit < len(units)-1 {
		speed /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", speed, units[unit])
}

func (st *SpeedTester) testProxy(name string, proxy *CProxy) *Result {
	result := &Result{
		ProxyName:   name,
		ProxyType:   proxy.Type().String(),
		ProxyConfig: proxy.Config,
	}

	// 使用context控制超时
	ctx, cancel := context.WithTimeout(context.Background(), st.config.Timeout)
	defer cancel()

	// 并发执行延迟测试
	latencyChan := make(chan *latencyResult, 1)
	go func() {
		latencyChan <- st.testLatency(proxy.Proxy)
	}()

	select {
	case latencyResult := <-latencyChan:
		result.Latency = latencyResult.avgLatency
		result.Jitter = latencyResult.jitter
		result.PacketLoss = latencyResult.packetLoss
	case <-ctx.Done():
		result.PacketLoss = 100
		return result
	}

	if result.PacketLoss == 100 {
		return result
	}

	var wg sync.WaitGroup
	downloadResults := make(chan *downloadResult, st.config.Concurrent)
	uploadResults := make(chan *downloadResult, st.config.Concurrent)

	downloadChunkSize := st.config.DownloadSize / st.config.Concurrent
	uploadChunkSize := st.config.UploadSize / st.config.Concurrent

	// 并发执行下载和上传测试
	for i := 0; i < st.config.Concurrent; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			downloadResults <- st.testDownload(proxy.Proxy, downloadChunkSize)
		}()
		go func() {
			defer wg.Done()
			uploadResults <- st.testUpload(proxy.Proxy, uploadChunkSize)
		}()
	}

	go func() {
		wg.Wait()
		close(downloadResults)
		close(uploadResults)
	}()

	var totalDownloadBytes, totalUploadBytes int64
	var totalDownloadTime, totalUploadTime time.Duration
	var downloadCount, uploadCount int

	for dr := range downloadResults {
		if dr != nil {
			totalDownloadBytes += dr.bytes
			totalDownloadTime += dr.duration
			downloadCount++
		}
	}

	for ur := range uploadResults {
		if ur != nil {
			totalUploadBytes += ur.bytes
			totalUploadTime += ur.duration
			uploadCount++
		}
	}

	if downloadCount > 0 {
		result.DownloadSize = float64(totalDownloadBytes)
		result.DownloadTime = totalDownloadTime / time.Duration(downloadCount)
		result.DownloadSpeed = float64(totalDownloadBytes) / result.DownloadTime.Seconds()
	}
	if uploadCount > 0 {
		result.UploadSize = float64(totalUploadBytes)
		result.UploadTime = totalUploadTime / time.Duration(uploadCount)
		result.UploadSpeed = float64(totalUploadBytes) / result.UploadTime.Seconds()
	}

	return result
}

type latencyResult struct {
	avgLatency time.Duration
	jitter     time.Duration
	packetLoss float64
}

func (st *SpeedTester) testLatency(proxy constant.Proxy) *latencyResult {
	client := st.createClient(proxy)
	latencies := make([]time.Duration, 0, 6)
	failedPings := 0

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 并发执行ping测试
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			resp, err := client.Get(fmt.Sprintf("%s/__down?bytes=0", st.config.ServerURL))
			if err != nil {
				mu.Lock()
				failedPings++
				mu.Unlock()
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				latencies = append(latencies, time.Since(start))
				mu.Unlock()
			} else {
				mu.Lock()
				failedPings++
				mu.Unlock()
			}
		}()
		time.Sleep(50 * time.Millisecond) // 稍微降低并发强度
	}

	wg.Wait()
	return calculateLatencyStats(latencies, failedPings)
}

type downloadResult struct {
	bytes    int64
	duration time.Duration
}

func (st *SpeedTester) testDownload(proxy constant.Proxy, size int) *downloadResult {
	client := st.createClient(proxy)
	start := time.Now()

	resp, err := client.Get(fmt.Sprintf("%s/__down?bytes=%d", st.config.ServerURL, size))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	downloadBytes, _ := io.Copy(io.Discard, resp.Body)

	return &downloadResult{
		bytes:    downloadBytes,
		duration: time.Since(start),
	}
}

func (st *SpeedTester) testUpload(proxy constant.Proxy, size int) *downloadResult {
	client := st.createClient(proxy)
	reader := NewZeroReader(size)

	start := time.Now()
	resp, err := client.Post(
		fmt.Sprintf("%s/__up", st.config.ServerURL),
		"application/octet-stream",
		reader,
	)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	return &downloadResult{
		bytes:    reader.WrittenBytes(),
		duration: time.Since(start),
	}
}

func (st *SpeedTester) createClient(proxy constant.Proxy) *http.Client {
	return &http.Client{
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
		},
	}
}

func calculateLatencyStats(latencies []time.Duration, failedPings int) *latencyResult {
	result := &latencyResult{
		packetLoss: float64(failedPings) / 6.0 * 100,
	}

	if len(latencies) == 0 {
		return result
	}

	var total time.Duration
	for _, l := range latencies {
		total += l
	}
	result.avgLatency = total / time.Duration(len(latencies))

	var variance float64
	for _, l := range latencies {
		diff := float64(l - result.avgLatency)
		variance += diff * diff
	}
	variance /= float64(len(latencies))
	result.jitter = time.Duration(math.Sqrt(variance))

	return result
}
