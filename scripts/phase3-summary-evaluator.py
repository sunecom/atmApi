#!/usr/bin/env python3
"""
Phase 3: 语义摘要质量评估脚本

评估指标：
1. 关键信息保留率（文件路径/金额/版本号/用户决策）
2. 工具调用成功率
3. Token 压缩比
4. 摘要延迟
5. 摘要成本
"""

import json
import re
import time
from typing import Dict, List, Tuple


class SummaryEvaluator:
    """语义摘要质量评估器"""
    
    # 关键信息正则模式
    PATTERNS = {
        'file_path': r'[/\\][\w/\\.-]+',  # 文件路径
        'amount': r'¥\d+\.?\d*|\$\d+\.?\d*|\d+\.\d{2}',  # 金额
        'version': r'v\d+\.\d+\.?\d*|版本\s*\d+',  # 版本号
        'decision': r'(决定|确认|同意|选择|采用)\s*[：:]\s*[^，。]+',  # 用户决策
        'error_code': r'(错误码|error|fail|exception)[：:\s]*[\w-]+',  # 错误码
    }
    
    def __init__(self):
        self.results = []
    
    def evaluate_sample(self, original: str, summary: str, tool_calls: List[Dict] = None) -> Dict:
        """
        评估单个样本的摘要质量
        
        Args:
            original: 原始历史消息
            summary: 生成的摘要
            tool_calls: 工具调用记录
        
        Returns:
            评估结果字典
        """
        result = {
            'original_length': len(original),
            'summary_length': len(summary),
            'compression_ratio': 0,
            'key_info_retention': {},
            'tool_call_success': None,
            'latency_ms': 0,
            'cost_cny': 0,
        }
        
        # 1. Token 压缩比（粗略估算：字符数 / 4）
        original_tokens = len(original) // 4
        summary_tokens = len(summary) // 4
        if original_tokens > 0:
            result['compression_ratio'] = (1 - summary_tokens / original_tokens) * 100
        
        # 2. 关键信息保留率
        for info_type, pattern in self.PATTERNS.items():
            original_matches = set(re.findall(pattern, original, re.IGNORECASE))
            summary_matches = set(re.findall(pattern, summary, re.IGNORECASE))
            
            if original_matches:
                retained = len(original_matches & summary_matches)
                total = len(original_matches)
                retention_rate = (retained / total) * 100 if total > 0 else 100
                result['key_info_retention'][info_type] = {
                    'retained': retained,
                    'total': total,
                    'rate': retention_rate
                }
        
        # 3. 工具调用成功率
        if tool_calls:
            success_count = sum(1 for tc in tool_calls if tc.get('success', False))
            result['tool_call_success'] = {
                'success': success_count,
                'total': len(tool_calls),
                'rate': (success_count / len(tool_calls)) * 100 if tool_calls else 100
            }
        
        return result
    
    def evaluate_batch(self, samples: List[Dict]) -> Dict:
        """
        批量评估样本
        
        Args:
            samples: [{'original': str, 'summary': str, 'tool_calls': List}, ...]
        
        Returns:
            汇总评估结果
        """
        all_results = []
        
        for sample in samples:
            result = self.evaluate_sample(
                sample['original'],
                sample['summary'],
                sample.get('tool_calls', [])
            )
            all_results.append(result)
        
        # 汇总统计
        summary = {
            'sample_count': len(samples),
            'avg_compression_ratio': 0,
            'avg_key_info_retention': {},
            'avg_tool_call_success': None,
            'pass_count': 0,
            'fail_count': 0,
        }
        
        if all_results:
            # 平均压缩比
            summary['avg_compression_ratio'] = sum(r['compression_ratio'] for r in all_results) / len(all_results)
            
            # 平均关键信息保留率
            for info_type in self.PATTERNS.keys():
                rates = [r['key_info_retention'].get(info_type, {}).get('rate', 0) 
                         for r in all_results 
                         if info_type in r['key_info_retention']]
                if rates:
                    summary['avg_key_info_retention'][info_type] = sum(rates) / len(rates)
            
            # 平均工具调用成功率
            success_rates = [r['tool_call_success']['rate'] 
                           for r in all_results 
                           if r['tool_call_success'] is not None]
            if success_rates:
                summary['avg_tool_call_success'] = sum(success_rates) / len(success_rates)
            
            # 通过/失败统计
            for result in all_results:
                if self._check_pass(result):
                    summary['pass_count'] += 1
                else:
                    summary['fail_count'] += 1
        
        return summary
    
    def _check_pass(self, result: Dict) -> bool:
        """
        检查单个样本是否通过准入门槛
        
        门槛标准：
        - 关键信息保留率 ≥ 95%
        - 工具调用成功率 ≥ 98%
        - Token 压缩比 ≥ 30%
        """
        # 关键信息保留率
        for info_type, stats in result['key_info_retention'].items():
            if stats['rate'] < 95:
                return False
        
        # 工具调用成功率
        if result['tool_call_success'] and result['tool_call_success']['rate'] < 98:
            return False
        
        # Token 压缩比
        if result['compression_ratio'] < 30:
            return False
        
        return True


def load_shadow_data(log_file: str) -> List[Dict]:
    """
    从日志文件加载 Shadow 摘要数据
    
    日志格式：
    [GLM52上下文] plan="xxx" ... shadow=true decision=xxx
    """
    samples = []
    
    with open(log_file, 'r', encoding='utf-8') as f:
        for line in f:
            if 'shadow=true' in line:
                # 解析日志行（简化版）
                # 实际需要从数据库或结构化日志中提取
                pass
    
    return samples


def main():
    """主函数"""
    print("=== Phase 3: 语义摘要质量评估 ===\n")
    
    # 示例样本（实际应从 Shadow 日志中提取）
    samples = [
        {
            'original': '用户决定采用方案A，文件路径为 /home/admin/test.py，金额 ¥100.50，版本 v2.1.0',
            'summary': '用户选择方案A，路径 /home/admin/test.py，金额 ¥100.50，版本 v2.1',
            'tool_calls': [
                {'name': 'read_file', 'success': True},
                {'name': 'edit_file', 'success': True}
            ]
        },
        {
            'original': '错误码 ERR-404，文件不存在于 /tmp/missing.txt',
            'summary': '错误 ERR-404，文件缺失',
            'tool_calls': [
                {'name': 'read_file', 'success': False}
            ]
        }
    ]
    
    evaluator = SummaryEvaluator()
    
    # 评估单个样本
    print("【样本 1 评估】")
    result1 = evaluator.evaluate_sample(
        samples[0]['original'],
        samples[0]['summary'],
        samples[0]['tool_calls']
    )
    print(json.dumps(result1, indent=2, ensure_ascii=False))
    
    print("\n【样本 2 评估】")
    result2 = evaluator.evaluate_sample(
        samples[1]['original'],
        samples[1]['summary'],
        samples[1]['tool_calls']
    )
    print(json.dumps(result2, indent=2, ensure_ascii=False))
    
    # 批量评估
    print("\n【批量评估汇总】")
    summary = evaluator.evaluate_batch(samples)
    print(json.dumps(summary, indent=2, ensure_ascii=False))
    
    # 准入门槛检查
    print("\n【准入门槛检查】")
    thresholds = {
        'key_info_retention': 95,
        'tool_call_success': 98,
        'compression_ratio': 30
    }
    
    for metric, threshold in thresholds.items():
        if metric == 'key_info_retention':
            avg_rates = [summary['avg_key_info_retention'].get(k, 0) 
                        for k in summary['avg_key_info_retention']]
            avg = sum(avg_rates) / len(avg_rates) if avg_rates else 0
        elif metric == 'tool_call_success':
            avg = summary['avg_tool_call_success'] or 0
        else:
            avg = summary['avg_compression_ratio']
        
        status = '✅ 通过' if avg >= threshold else '❌ 未通过'
        print(f"{metric}: {avg:.1f}% (门槛 {threshold}%) {status}")


if __name__ == '__main__':
    main()
