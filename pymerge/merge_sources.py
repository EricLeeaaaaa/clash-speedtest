import sys
import yaml
import requests
import socket
import urllib.parse
from yaml.loader import FullLoader

class Site:
    def __init__(self, config: dict, verbose: str = 'normal'):
        # 自动从URL推断名称
        self.url = config.get('url')
        self.name = config.get('name') or self._generate_name_from_url(self.url)
        
        # 默认配置
        self.group = config.get('group', 'auto')
        self.inclusion = config.get('inclusion', [])
        self.exclusion = config.get('exclusion', [])
        self.dedup = config.get('dedup', True)
        
        self.verbose = verbose
        self.nodes = []
        self.data = None

        self._fetch_proxy_list()

    def _generate_name_from_url(self, url):
        # 从URL生成一个友好的名称
        parsed = urllib.parse.urlparse(url)
        
        # 处理GitHub raw链接
        if 'raw.githubusercontent.com' in parsed.netloc:
            # 分割路径，获取用户名和仓库名
            parts = parsed.path.split('/')
            if len(parts) > 2:
                # 返回仓库名
                return parts[2]
        
        # 其他情况返回域名的第二级
        return parsed.netloc.split('.')[-2] if parsed.netloc else 'Unknown'

    def _fetch_proxy_list(self):
        try:
            headers = {
                "User-Agent": "ClashForAndroid/2.5.12",
            }
            r = requests.get(self.url, headers=headers, timeout=10)
            r.raise_for_status()
            
            self.data = yaml.load(r.text, Loader=FullLoader)
            
            if self.verbose != 'quiet':
                self.log(f"成功获取订阅: {len(self.data.get('proxies', [])) if self.data else 0} 个节点")
        
        except Exception as e:
            self.data = None
            if self.verbose != 'quiet':
                self.log(f"订阅获取失败: {e}")

    def purge(self):
        if not self.data or 'proxies' not in self.data:
            if self.verbose != 'quiet':
                self.log("No proxies found")
            return

        self.nodes = self.data['proxies']
        nodes_good = []

        # 黑名单过滤
        if self.exclusion:
            nodes_good = [
                node for node in self.nodes 
                if not any(k.lower() in node['name'].lower() or k.lower() in node['server'].lower() for k in self.exclusion)
            ]
            self.nodes = nodes_good
            nodes_good = []

        # 白名单过滤
        if self.inclusion:
            nodes_good = [
                node for node in self.nodes 
                if any(k.lower() in node['name'].lower() or k.lower() in node['server'].lower() for k in self.inclusion)
            ]
            self.nodes = nodes_good
            nodes_good = []

        # 去重
        if self.dedup:
            used = set()
            for node in self.nodes:
                try:
                    ip = socket.getaddrinfo(node['server'], None)[0][4][0]
                    p = (ip, node['port'])
                    if p not in used:
                        used.add(p)
                        nodes_good.append(node)
                except:
                    if self.verbose != 'quiet':
                        self.log(f"Failed to resolve node {node['name']}: {node['server']}")
            self.nodes = nodes_good

    def get_titles(self):
        return [x['name'] for x in self.nodes]

    def log(self, message: str):
        if self.verbose != 'quiet':
            print(f"[{self.name}] {message}")

def merge_sources(sources_file, output_file='merged_sources.yaml', verbose='normal'):
    """
    从多个订阅源合并代理节点
    
    :param sources_file: 订阅源配置文件
    :param output_file: 输出的合并订阅文件
    :param verbose: 日志详细程度
    :return: 合并后的代理节点列表
    """
    # 读取站点配置
    with open(sources_file, "r", encoding="utf-8") as f:
        sites_config = yaml.load(f, Loader=FullLoader)
        # 确保使用 sources 列表
        sites_config = sites_config.get('sources', [])

    # 初始化代理列表
    merged_proxies = []
    
    # 处理代理站点
    proxy_count = 0
    for site_config in sites_config:
        site = Site(site_config, verbose)
        if site.data is not None:
            try:
                site.purge()
                if site.nodes:
                    merged_proxies += site.nodes
                    proxy_count += len(site.nodes)
            except Exception as e:
                if verbose != 'quiet':
                    print(f"Failed to process {site.name}: {e}")
    
    # 对节点名去重
    merged_proxies = list({x['name']: x for x in merged_proxies}.values())

    # 创建输出结构
    output_config = {
        'proxies': merged_proxies
    }

    # 写入输出文件
    with open(output_file, "w", encoding="utf-8") as f:
        f.write(yaml.dump(output_config, default_flow_style=False, allow_unicode=True))

    # 输出日志
    if verbose == 'quiet':
        print(f"已生成包含 {proxy_count} 个节点的订阅文件：{output_file}")
    else:
        print(f"订阅文件已生成：{output_file}")

def main():
    # 检查参数
    if len(sys.argv) < 2 or len(sys.argv) > 4:
        print("Usage:")
        print("    python merge_sources.py <sources_config> [output] [verbose]")
        print("Example:")
        print("    python merge_sources.py sources.yaml merged_sources.yaml [quiet/normal/verbose]")
        sys.exit(1)

    # 决定输出和日志详细程度
    sources_file = sys.argv[1]
    output_file = sys.argv[2] if len(sys.argv) >= 3 and not sys.argv[2].startswith(('quiet', 'normal', 'verbose')) else "merged_sources.yaml"
    verbose = 'normal' if len(sys.argv) < 4 else sys.argv[3]

    # 合并订阅
    merge_sources(sources_file, output_file, verbose)

if __name__ == "__main__":
    main()
