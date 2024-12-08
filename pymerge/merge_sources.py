import sys
import yaml
import asyncio
import aiohttp
import socket
import urllib.parse
from typing import List, Dict, Optional, Set, Any
from dataclasses import dataclass, field
from pathlib import Path
from yaml.loader import FullLoader
from concurrent.futures import ThreadPoolExecutor

# 常量定义
DEFAULT_GROUP = "PROXY"
DEFAULT_TIMEOUT = 10
USER_AGENT = "ClashForAndroid/2.5.12"
GITHUB_RAW_DOMAIN = 'raw.githubusercontent.com'

@dataclass
class SiteConfig:
    """订阅站点配置数据类"""
    url: str
    name: Optional[str] = None
    group: str = DEFAULT_GROUP
    inclusion: List[str] = field(default_factory=list)
    exclusion: List[str] = field(default_factory=list)
    dedup: bool = True

class ProxyNode:
    """代理节点类，用于处理单个节点的逻辑"""
    def __init__(self, node_data: Dict[str, Any]):
        # 验证必需字段
        required_fields = ["name", "server", "port", "type"]
        for field in required_fields:
            if field not in node_data:
                raise ValueError(f"Missing required field: {field}")

        self.name: str = node_data['name']
        self.server: str = node_data['server']
        self.port: int = node_data['port']
        self.type: str = node_data['type']
        self.data: Dict[str, Any] = node_data
        self._resolved_ip: Optional[str] = None

        # REALITY协议特殊处理
        if self.type.lower() == "vless" and "reality-opts" in node_data:
            reality_opts = node_data["reality-opts"]
            if not isinstance(reality_opts, dict):
                raise ValueError("Invalid reality-opts format")
            if "short-id" not in reality_opts or not reality_opts["short-id"]:
                raise ValueError("Invalid REALITY short ID")

    async def resolve_ip(self) -> Optional[str]:
        """异步解析节点IP地址"""
        if not self._resolved_ip:
            try:
                loop = asyncio.get_event_loop()
                with ThreadPoolExecutor() as executor:
                    self._resolved_ip = await loop.run_in_executor(
                        executor,
                        lambda: socket.getaddrinfo(self.server, None)[0][4][0]
                    )
            except Exception:
                return None
        return self._resolved_ip

    def matches_filter(self, keywords: List[str]) -> bool:
        """检查节点是否匹配关键词过滤"""
        if not keywords:
            return True
        text = f"{self.name.lower()} {self.server.lower()}"
        return any(k.lower() in text for k in keywords)

    def __hash__(self) -> int:
        return hash((self.server, self.port))

class Site:
    """订阅站点类，处理单个订阅源的所有操作"""
    def __init__(self, config: Dict[str, Any], verbose: str = 'normal'):
        self.config = SiteConfig(**config)
        self.verbose = verbose
        self.nodes: List[ProxyNode] = []
        self._raw_data: Optional[Dict[str, Any]] = None

    def _generate_name_from_url(self) -> str:
        """从URL生成站点名称"""
        parsed = urllib.parse.urlparse(self.config.url)
        
        if GITHUB_RAW_DOMAIN in parsed.netloc:
            parts = parsed.path.split('/')
            return parts[2] if len(parts) > 2 else 'Unknown'
            
        return parsed.netloc.split('.')[-2] if parsed.netloc else 'Unknown'

    @property
    def name(self) -> str:
        """获取站点名称，如果未配置则从URL生成"""
        return self.config.name or self._generate_name_from_url()

    async def fetch_proxy_list(self) -> None:
        """异步获取代理列表"""
        headers = {"User-Agent": USER_AGENT}
        try:
            async with aiohttp.ClientSession() as session:
                async with session.get(
                    self.config.url, 
                    headers=headers, 
                    timeout=DEFAULT_TIMEOUT
                ) as response:
                    response.raise_for_status()
                    content = await response.text()
                    self._raw_data = yaml.load(content, Loader=FullLoader)
                    
                    if self.verbose != 'quiet':
                        self.log(f"成功获取订阅: {len(self._raw_data.get('proxies', [])) if self._raw_data else 0} 个节点")
        
        except Exception as e:
            self._raw_data = None
            if self.verbose != 'quiet':
                self.log(f"订阅获取失败: {e}")

    async def process_nodes(self) -> None:
        """处理和过滤代理节点"""
        if not self._raw_data or 'proxies' not in self._raw_data:
            if self.verbose != 'quiet':
                self.log("未找到代理节点")
            return

        # 转换为ProxyNode对象，处理无效节点
        valid_nodes = []
        for node_data in self._raw_data['proxies']:
            try:
                node = ProxyNode(node_data)
                valid_nodes.append(node)
            except ValueError as e:
                if self.verbose != 'quiet':
                    self.log(f"跳过无效节点 {node_data.get('name', 'unknown')}: {str(e)}")
                continue

        self.nodes = valid_nodes

        # 应用过滤规则
        if self.config.exclusion:
            self.nodes = [
                node for node in self.nodes 
                if not node.matches_filter(self.config.exclusion)
            ]

        if self.config.inclusion:
            self.nodes = [
                node for node in self.nodes 
                if node.matches_filter(self.config.inclusion)
            ]

        # 去重处理
        if self.config.dedup:
            unique_nodes: Set[ProxyNode] = set()
            filtered_nodes: List[ProxyNode] = []
            
            for node in self.nodes:
                ip = await node.resolve_ip()
                if ip and (ip, node.port) not in unique_nodes:
                    unique_nodes.add((ip, node.port))
                    filtered_nodes.append(node)
                elif self.verbose != 'quiet' and not ip:
                    self.log(f"无法解析节点 {node.name}: {node.server}")
            
            self.nodes = filtered_nodes

    def get_proxy_data(self) -> List[Dict[str, Any]]:
        """获取处理后的节点数据"""
        return [node.data for node in self.nodes]

    def log(self, message: str) -> None:
        """输出日志信息"""
        if self.verbose != 'quiet':
            print(f"[{self.name}] {message}")

async def merge_sources(
    sources_file: str,
    output_file: str = 'merged_sources.yaml',
    verbose: str = 'normal'
) -> List[Dict[str, Any]]:
    """
    异步从多个订阅源合并代理节点
    
    Args:
        sources_file: 订阅源配置文件路径
        output_file: 输出的合并订阅文件路径
        verbose: 日志详细程度 ('quiet'/'normal'/'verbose')
    
    Returns:
        合并后的代理节点列表
    """
    # 读取配置文件
    with open(sources_file, "r", encoding="utf-8") as f:
        config = yaml.load(f, Loader=FullLoader)
        sites_config = config.get('sources', [])

    # 初始化站点
    sites = [Site(site_config, verbose) for site_config in sites_config]
    
    # 并发获取代理列表
    await asyncio.gather(*(site.fetch_proxy_list() for site in sites))
    
    # 处理节点
    await asyncio.gather(*(site.process_nodes() for site in sites))
    
    # 合并节点
    merged_proxies = []
    proxy_names = set()
    
    for site in sites:
        for proxy in site.get_proxy_data():
            if proxy['name'] not in proxy_names:
                proxy_names.add(proxy['name'])
                merged_proxies.append(proxy)

    # 生成输出配置
    output_config = {'proxies': merged_proxies}
    
    # 写入文件
    with open(output_file, "w", encoding="utf-8") as f:
        yaml.dump(output_config, f, default_flow_style=False, allow_unicode=True)

    # 输出日志
    if verbose == 'quiet':
        print(f"已生成包含 {len(merged_proxies)} 个节点的订阅文件：{output_file}")
    else:
        print(f"订阅文件已生成：{output_file}")

    return merged_proxies

async def async_main() -> None:
    """异步主函数"""
    if len(sys.argv) < 2 or len(sys.argv) > 4:
        print("用法:")
        print("    python merge_sources.py <sources_config> [output] [verbose]")
        print("示例:")
        print("    python merge_sources.py sources.yaml merged_sources.yaml [quiet/normal/verbose]")
        sys.exit(1)

    sources_file = sys.argv[1]
    output_file = sys.argv[2] if len(sys.argv) >= 3 and not sys.argv[2].startswith(('quiet', 'normal', 'verbose')) else "merged_sources.yaml"
    verbose = sys.argv[-1] if len(sys.argv) >= 3 and sys.argv[-1] in ('quiet', 'normal', 'verbose') else 'normal'

    await merge_sources(sources_file, output_file, verbose)

def main():
    """主函数入口"""
    asyncio.run(async_main())

if __name__ == "__main__":
    main()
