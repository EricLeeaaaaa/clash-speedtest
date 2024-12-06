package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/faceair/clash-speedtest/speedtester"
	"github.com/metacubex/mihomo/log"
	"github.com/schollz/progressbar/v3"
	"gopkg.in/yaml.v3"
)

var (
	configPathsConfig = flag.String("c", "", "config file path, also support http(s) url")
	filterRegexConfig = flag.String("f", ".+", "filter proxies by name, use regexp")
	serverURL         = flag.String("server-url", "https://speed.cloudflare.com", "server url")
	downloadSize      = flag.Int("download-size", 10*1024*1024, "download size for testing proxies")
	timeout           = flag.Duration("timeout", time.Second*5, "timeout for testing proxies")
	concurrent        = flag.Int("concurrent", 4, "concurrent testing size")
	outputPath        = flag.String("output", "", "output config file path")
	maxLatency        = flag.Duration("max-latency", 800*time.Millisecond, "filter latency greater than this value")
	minSpeed          = flag.Float64("min-speed", 5, "filter speed less than this value(unit: MB/s)")
	sourcesFile       = flag.String("s", "", "sources file path containing multiple yaml sources")
)

type Source struct {
	Name string
	URL  string
}

func parseSources(path string) ([]Source, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var sources []Source
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		url := strings.TrimSpace(strings.Join(parts[1:], ":"))
		sources = append(sources, Source{Name: name, URL: url})
	}

	return sources, scanner.Err()
}

func main() {
	flag.Parse()
	log.SetLevel(log.SILENT)

	if *configPathsConfig == "" && *sourcesFile == "" {
		log.Fatalln("%s", "please specify either the configuration file (-c) or sources file (-s)")
	}

	var sources []Source
	var err error

	if *sourcesFile != "" {
		sources, err = parseSources(*sourcesFile)
		if err != nil {
			log.Fatalln("%s: %v", "failed to parse sources file", err)
		}
	}

	var allResults []*speedtester.Result

	if len(sources) > 0 {
		for _, source := range sources {
			fmt.Printf("\n测试源: %s\n", source.Name)

			speedTester := speedtester.New(&speedtester.Config{
				ConfigPaths:  source.URL,
				FilterRegex:  *filterRegexConfig,
				ServerURL:    *serverURL,
				DownloadSize: *downloadSize,
				Timeout:      *timeout,
				Concurrent:   *concurrent,
				NamePrefix:   source.Name + "-",
			})

			proxies, err := speedTester.LoadProxies()
			if err != nil {
				log.Warnln("load proxies failed for %s: %v", source.Name, err)
				continue
			}

			bar := progressbar.Default(int64(len(proxies)), "测试中...")
			results := make([]*speedtester.Result, 0)
			speedTester.TestProxies(proxies, func(result *speedtester.Result) {
				bar.Add(1)
				bar.Describe(result.ProxyName)
				results = append(results, result)
			})

			allResults = append(allResults, results...)
		}
	} else {
		speedTester := speedtester.New(&speedtester.Config{
			ConfigPaths:  *configPathsConfig,
			FilterRegex:  *filterRegexConfig,
			ServerURL:    *serverURL,
			DownloadSize: *downloadSize,
			Timeout:      *timeout,
			Concurrent:   *concurrent,
		})

		proxies, err := speedTester.LoadProxies()
		if err != nil {
			log.Fatalln("%s: %v", "load proxies failed", err)
		}

		bar := progressbar.Default(int64(len(proxies)), "测试中...")
		results := make([]*speedtester.Result, 0)
		speedTester.TestProxies(proxies, func(result *speedtester.Result) {
			bar.Add(1)
			bar.Describe(result.ProxyName)
			results = append(results, result)
		})

		allResults = results
	}

	// 按下载速度排序
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].DownloadSpeed > allResults[j].DownloadSpeed
	})

	// 简化输出结果
	fmt.Printf("\n测速结果:\n\n")
	for i, result := range allResults {
		speedInMB := result.DownloadSpeed / (1024 * 1024)
		fmt.Printf("%d. %s\n   类型: %s, 速度: %.2f MB/s\n\n",
			i+1,
			result.ProxyName,
			result.ProxyType,
			speedInMB,
		)
	}

	if *outputPath != "" {
		err = saveConfig(allResults)
		if err != nil {
			log.Fatalln("%s: %v", "save config file failed", err)
		}
		fmt.Printf("\n配置文件已保存至: %s\n", *outputPath)
	}
}

func saveConfig(results []*speedtester.Result) error {
	filteredResults := make([]*speedtester.Result, 0)
	for _, result := range results {
		if *maxLatency > 0 && result.Latency > *maxLatency {
			continue
		}
		if *minSpeed > 0 && float64(result.DownloadSpeed)/(1024*1024) < *minSpeed {
			continue
		}
		filteredResults = append(filteredResults, result)
	}

	proxies := make([]map[string]any, 0)
	for _, result := range filteredResults {
		proxies = append(proxies, result.ProxyConfig)
	}

	config := &speedtester.RawConfig{
		Proxies: proxies,
	}
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile(*outputPath, yamlData, 0o644)
}
