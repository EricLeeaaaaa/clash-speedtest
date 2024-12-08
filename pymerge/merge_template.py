import sys
from typing import Dict, List, Any, Optional
from pathlib import Path
import yaml
from yaml.loader import FullLoader

def load_yaml_file(file_path: str) -> Dict[str, Any]:
    """
    加载并解析YAML文件
    
    Args:
        file_path: YAML文件路径
    
    Returns:
        解析后的YAML数据
        
    Raises:
        FileNotFoundError: 文件不存在
        yaml.YAMLError: YAML解析错误
    """
    try:
        with open(file_path, "r", encoding="utf-8") as f:
            return yaml.load(f, Loader=FullLoader)
    except FileNotFoundError:
        raise FileNotFoundError(f"找不到文件: {file_path}")
    except yaml.YAMLError as e:
        raise yaml.YAMLError(f"YAML解析错误: {e}")

def create_proxy_group(proxies: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """
    创建代理分组配置
    
    Args:
        proxies: 代理节点列表
    
    Returns:
        代理分组配置列表
    """
    return [{
        "name": "PROXY",
        "type": "select",
        "proxies": [p['name'] for p in proxies]
    }]

def merge_template(
    merged_sources: str,
    template_file: str,
    output_file: str = 'output.yaml',
    verbose: str = 'normal'
) -> None:
    """
    将合并的订阅与模板合并
    
    Args:
        merged_sources: 已合并的订阅文件路径
        template_file: 模板配置文件路径
        output_file: 输出的最终配置文件路径
        verbose: 日志详细程度 ('quiet'/'normal'/'verbose')
    
    Raises:
        FileNotFoundError: 输入文件不存在
        yaml.YAMLError: YAML解析错误
    """
    try:
        # 加载合并的订阅
        sources_config = load_yaml_file(merged_sources)
        proxies = sources_config.get('proxies', [])
        
        # 加载模板配置
        config = load_yaml_file(template_file)
        
        # 更新配置
        proxy_groups = create_proxy_group(proxies)
        config.update({
            'proxies': proxies,
            'proxy-groups': proxy_groups
        })
        
        # 写入输出文件
        output_path = Path(output_file)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        
        with open(output_path, "w", encoding="utf-8") as f:
            yaml.dump(config, f, default_flow_style=False, allow_unicode=True)
        
        # 输出日志
        if verbose == 'quiet':
            print(f"已生成包含 {len(proxies)} 个节点的配置文件：{output_file}")
        else:
            print(f"配置文件已生成：{output_file}")
            
    except Exception as e:
        print(f"错误: {str(e)}", file=sys.stderr)
        sys.exit(1)

def main() -> None:
    """主函数入口"""
    if len(sys.argv) < 3 or len(sys.argv) > 5:
        print("用法:")
        print("    python merge_template.py <merged_sources> <template> [output] [verbose]")
        print("示例:")
        print("    python merge_template.py merged_sources.yaml template.yaml output.yaml [quiet/normal/verbose]")
        sys.exit(1)

    # 解析命令行参数
    merged_sources = sys.argv[1]
    template_file = sys.argv[2]
    output_file = (
        sys.argv[3] 
        if len(sys.argv) >= 4 and not sys.argv[3].startswith(('quiet', 'normal', 'verbose'))
        else "output.yaml"
    )
    verbose = (
        sys.argv[-1]
        if len(sys.argv) >= 4 and sys.argv[-1] in ('quiet', 'normal', 'verbose')
        else 'normal'
    )

    merge_template(merged_sources, template_file, output_file, verbose)

if __name__ == "__main__":
    main()
