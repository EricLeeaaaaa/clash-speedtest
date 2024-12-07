# Clash-SpeedTest

基于 Clash/Mihomo 核心的测速工具，快速测试你的节点速度。

Features:
1. 无需额外的配置，直接将 Clash/Mihomo 配置本地文件路径或者订阅地址作为参数传入即可
2. 支持 Proxies 和 Proxy Provider 中定义的全部类型代理节点，兼容性跟 Mihomo 一致
3. 不依赖额外的 Clash/Mihomo 进程实例，单一工具即可完成测试
4. 代码简单而且开源，不发布构建好的二进制文件，保证你的节点安全
5. 支持并发测试，提高测速效率
6. 可自定义测速服务器和下载大小，灵活适应不同需求
7. 提供独立的测速服务器模块，支持自建测速服务

<img width="1332" alt="image" src="https://github.com/user-attachments/assets/fdc47ec5-b626-45a3-a38a-6d88c326c588">

## 使用方法

```bash
# 支持从源码安装，或从 Release 里下载由 Github Action 自动构建的二进制文件
> go install github.com/faceair/clash-speedtest@latest

# 查看帮助
> clash-speedtest -h
Usage of clash-speedtest:
  -c string
        configuration file path, also support http(s) url
  -f string
        filter proxies by name, use regexp (default ".*")
  -server-url string
        server url for testing proxies (default "https://speed.cloudflare.com")
  -download-size int
        download size for testing proxies (default 50MB)
  -timeout duration
        timeout for testing proxies (default 5s)
  -concurrent int
        download concurrent size (default 4)
  -output string
        output config file path (default "")
  -min-speed float
        filter speed less than this value(unit: MB/s) (default 5)

# 演示：

# 1. 测试全部节点，使用 HTTP 订阅地址
# 请在订阅地址后面带上 flag=meta 参数，否则无法识别出节点类型
> clash-speedtest -c 'https://domain.com/api/v1/client/subscribe?token=secret&flag=meta'

# 2. 测试香港节点，使用正则表达式过滤，使用本地文件
> clash-speedtest -c ~/.config/clash/config.yaml -f 'HK|港'
序号	节点名称                                  	类型  	下载速度
1.	Premium|广港|IEPL|05                        	Vmess	3.87MB/s
2.	Premium|广港|IEPL|03                        	Vmess	2.62MB/s
3.	Premium|广港|IEPL|04                        	Vmess	1.46MB/s
4.	Premium|广港|IEPL|01                        	Vmess	484.80KB/s
5.	Premium|广港|IEPL|02                        	Vmess	N/A

# 3. 当然你也可以混合使用
> clash-speedtest -c "https://domain.com/api/v1/client/subscribe?token=secret&flag=meta,/home/.config/clash/config.yaml"

# 4. 筛选出下载速度大于 5MB/s 的节点，并输出到 filtered.yaml
> clash-speedtest -c "https://domain.com/api/v1/client/subscribe?token=secret&flag=meta" -output filtered.yaml -min-speed 5
# 筛选后的配置文件可以直接粘贴到 Clash/Mihomo 中使用，或是贴到 Github\Gist 上通过 Proxy Provider 引用。
```

## 测速原理

通过 HTTP GET 请求下载指定大小的文件，默认使用 https://speed.cloudflare.com (50MB) 进行测试，计算下载时间得到下载速度。测试支持并发下载，默认并发数为4，可通过 -concurrent 参数调整。

测试结果：
1. 下载速度 是指下载指定大小文件的速度。当这个数值越高时表明节点的出口带宽越大。
2. 节点类型 显示了代理节点的协议类型，如 Vmess、Shadowsocks 等。

请注意：
1. 下载速度高不一定意味着实际使用体验会更好。实际体验还受到延迟、丢包率等因素的影响。
2. 测试结果可能会因网络环境、时间等因素而有所波动。建议在不同时间段多次测试以获得更准确的结果。

Cloudflare 是全球知名的 CDN 服务商，其提供的测速服务器到海外绝大部分的节点速度都很快，一般情况下都没有必要自建测速服务器。

如果你不想使用 Cloudflare 的测速服务器，可以自己搭建一个测速服务器。

```shell
# 在您需要进行测速的服务器上安装和启动测速服务器
> go install github.com/faceair/clash-speedtest/download-server@latest
> download-server

# 此时在本地使用 http://your-server-ip:8080 作为 server-url 即可
> clash-speedtest --server-url "http://your-server-ip:8080"
```

测速服务器功能：
1. 提供下载测试接口（`/__down`），支持生成指定大小的二进制文件。
2. 提供上传测试接口（`/__up`），接收上传数据并丢弃。

## 注意事项

1. 请确保你有权限测试这些节点。频繁的大流量测试可能会导致被封号。
2. 测试结果仅供参考。实际使用体验可能会因为网络环境、时间等因素而有所不同。
3. 使用 -output 参数输出筛选后的配置文件时，请注意保护你的节点信息安全。

## License

[MIT](LICENSE)
