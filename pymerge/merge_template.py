import sys
import yaml
from yaml.loader import FullLoader

def merge_template(merged_sources, template_file, output_file='output.yaml', verbose='normal'):
    """
    将合并的订阅与模板合并
    
    :param merged_sources: 已合并的订阅文件
    :param template_file: 模板配置文件
    :param output_file: 输出的最终配置文件
    :param verbose: 日志详细程度
    """
    # 读取合并的订阅
    with open(merged_sources, "r", encoding="utf-8") as f:
        sources_config = yaml.load(f, Loader=FullLoader)
        proxies = sources_config.get('proxies', [])

    # 读取模板配置
    with open(template_file, "r", encoding="utf-8") as f:
        config = yaml.load(f, Loader=FullLoader)

    # 默认创建一个 PROXY 分组
    proxy_groups = [{"name": "PROXY", "type": "select", "proxies": []}]
    
    # 将所有节点添加到 PROXY 分组
    proxy_groups[0]['proxies'] = [p['name'] for p in proxies]

    # 更新配置
    config['proxies'] = proxies
    config['proxy-groups'] = proxy_groups

    # 写入输出文件
    with open(output_file, "w", encoding="utf-8") as f:
        f.write(yaml.dump(config, default_flow_style=False, allow_unicode=True))

    # 输出日志
    if verbose == 'quiet':
        print(f"已生成包含 {len(proxies)} 个节点的配置文件：{output_file}")
    else:
        print(f"配置文件已生成：{output_file}")

def main():
    # 检查参数
    if len(sys.argv) < 3 or len(sys.argv) > 5:
        print("Usage:")
        print("    python3 merge_template.py <merged_sources> <template> [output] [verbose]")
        print("Example:")
        print("    python3 merge_template.py merged_sources.yaml template.yaml output.yaml [quiet/normal/verbose]")
        sys.exit(1)

    # 决定输出和日志详细程度
    merged_sources = sys.argv[1]
    template_file = sys.argv[2]
    output_file = sys.argv[3] if len(sys.argv) >= 4 and not sys.argv[3].startswith(('quiet', 'normal', 'verbose')) else "output.yaml"
    verbose = 'normal' if len(sys.argv) < 5 or sys.argv[-1].startswith(('quiet', 'normal', 'verbose')) else sys.argv[-1]

    # 合并模板
    merge_template(merged_sources, template_file, output_file, verbose)

if __name__ == "__main__":
    main()
