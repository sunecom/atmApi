#!/usr/bin/env python3
"""
GLM-5.2 渠道能力实测脚本 (Phase 0A)

测试矩阵：
- 输入长度：16K / 32K / 64K / 128K tokens
- 输出预算：8K / 16K / 32K tokens
- 流式模式：stream=true / stream=false
- 渠道：OpenRouter / 硅基流动 / 词元

记录：TTFT、总时长、成功率、终态、实际 Provider、费用
"""

import requests
import time
import json
import sys
from datetime import datetime

# 配置
API_BASE = "http://localhost:3300/v1"
TEST_TOKEN = "atm-test-glm52"  # 从数据库获取的测试 Token

# 测试矩阵
INPUT_SIZES = [16000, 32000, 64000, 128000]  # tokens
OUTPUT_SIZES = [8192, 16384, 32768]  # tokens
STREAM_MODES = [True, False]

# 渠道列表（通过指定 channel 参数测试）
# 实际上 atmApi 会自动路由，我们通过日志观察实际渠道
CHANNELS = ["OpenRouter", "硅基流动", "词元"]


def generate_test_input(target_tokens: int) -> list:
    """
    生成指定 token 数量的测试输入
    
    粗略估算：1 token ≈ 4 字符（英文）或 1.5 字符（中文）
    我们用英文，更可控
    """
    # 每个 token 约 4 字符，留一些余量
    target_chars = target_tokens * 3
    
    # 生成重复文本直到达到目标长度
    base_text = "This is a test paragraph for GLM-5.2 context window capability testing. "
    repeated_text = (base_text * (target_chars // len(base_text) + 1))[:target_chars]
    
    messages = [
        {"role": "system", "content": "You are a helpful assistant. Please summarize the following text in one sentence."},
        {"role": "user", "content": f"Please summarize this long text (approximately {target_tokens} tokens):\n\n{repeated_text}"}
    ]
    
    return messages


def estimate_tokens(messages: list) -> int:
    """粗略估算 token 数"""
    total_chars = sum(len(json.dumps(msg)) for msg in messages)
    return total_chars // 4  # 粗略估算


def test_request(messages: list, max_tokens: int, stream: bool, timeout: int = 300) -> dict:
    """
    发送测试请求并记录结果
    
    返回：
    {
        "success": bool,
        "ttft_ms": int,
        "total_time_ms": int,
        "status_code": int,
        "error": str,
        "usage": dict,
        "provider": str,
        "cost": float
    }
    """
    headers = {
        "Authorization": f"Bearer {TEST_TOKEN}",
        "Content-Type": "application/json"
    }
    
    payload = {
        "model": "glm-5.2",
        "messages": messages,
        "max_tokens": max_tokens,
        "stream": stream
    }
    
    start_time = time.time()
    first_token_time = None
    
    try:
        response = requests.post(
            f"{API_BASE}/chat/completions",
            headers=headers,
            json=payload,
            stream=stream,
            timeout=timeout
        )
        
        result = {
            "success": response.status_code == 200,
            "status_code": response.status_code,
            "ttft_ms": None,
            "total_time_ms": None,
            "error": None,
            "usage": None,
            "provider": None,
            "cost": None
        }
        
        if stream:
            # 流式响应
            first_chunk = True
            for line in response.iter_lines():
                if line:
                    if first_chunk:
                        first_token_time = time.time()
                        result["ttft_ms"] = int((first_token_time - start_time) * 1000)
                        first_chunk = False
                    
                    line_str = line.decode('utf-8')
                    if line_str.startswith('data: ') and line_str != 'data: [DONE]':
                        try:
                            data = json.loads(line_str[6:])
                            # 提取 usage（如果有的话）
                            if 'usage' in data:
                                result["usage"] = data['usage']
                        except:
                            pass
        else:
            # 非流式响应
            data = response.json()
            result["ttft_ms"] = int((time.time() - start_time) * 1000)
            
            if response.status_code == 200:
                result["usage"] = data.get('usage', {})
                # 尝试从响应头提取 provider 信息
                result["provider"] = response.headers.get('X-ATM-Provider', 'unknown')
            else:
                result["error"] = data.get('error', {}).get('message', str(data))
        
        result["total_time_ms"] = int((time.time() - start_time) * 1000)
        return result
        
    except requests.exceptions.Timeout:
        return {
            "success": False,
            "status_code": 0,
            "ttft_ms": None,
            "total_time_ms": int((time.time() - start_time) * 1000),
            "error": "TIMEOUT",
            "usage": None,
            "provider": None,
            "cost": None
        }
    except Exception as e:
        return {
            "success": False,
            "status_code": 0,
            "ttft_ms": None,
            "total_time_ms": int((time.time() - start_time) * 1000),
            "error": str(e),
            "usage": None,
            "provider": None,
            "cost": None
        }


def run_test_suite():
    """运行完整测试套件"""
    results = []
    
    print(f"=== GLM-5.2 渠道能力实测 ===")
    print(f"开始时间：{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"API: {API_BASE}")
    print(f"Token: {TEST_TOKEN[:20]}...")
    print()
    
    total_tests = len(INPUT_SIZES) * len(OUTPUT_SIZES) * len(STREAM_MODES)
    current_test = 0
    
    for input_size in INPUT_SIZES:
        for output_size in OUTPUT_SIZES:
            for stream in STREAM_MODES:
                current_test += 1
                test_name = f"input={input_size//1000}K output={output_size//1000}K stream={stream}"
                
                print(f"[{current_test}/{total_tests}] {test_name}")
                
                # 生成测试输入
                messages = generate_test_input(input_size)
                estimated = estimate_tokens(messages)
                
                print(f"  估算输入: {estimated} tokens")
                
                # 发送请求
                result = test_request(messages, output_size, stream)
                
                # 记录结果
                result["test_name"] = test_name
                result["input_size"] = input_size
                result["output_size"] = output_size
                result["stream"] = stream
                result["estimated_input_tokens"] = estimated
                
                results.append(result)
                
                # 输出结果
                if result["success"]:
                    print(f"  ✅ 成功 | TTFT={result['ttft_ms']}ms | 总时长={result['total_time_ms']}ms")
                    if result["usage"]:
                        print(f"     用量: input={result['usage'].get('prompt_tokens', 0)} output={result['usage'].get('completion_tokens', 0)}")
                else:
                    error_msg = result['error'][:100] if result['error'] else "unknown"
                    print(f"  ❌ 失败 | 状态={result['status_code']} | 错误={error_msg}")
                
                print()
                
                # 避免请求过快
                time.sleep(1)
    
    # 生成报告
    generate_report(results)


def generate_report(results: list):
    """生成测试报告"""
    report_file = f"/tmp/glm52-capability-test-{datetime.now().strftime('%Y%m%d-%H%M%S')}.md"
    
    with open(report_file, 'w') as f:
        f.write("# GLM-5.2 渠道能力实测报告\n\n")
        f.write(f"**测试时间**: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n")
        f.write(f"**测试环境**: 开发机艾隆 :3300\n\n")
        f.write(f"**测试 Token**: {TEST_TOKEN[:20]}...\n\n")
        
        f.write("## 测试矩阵\n\n")
        f.write(f"- 输入长度: {', '.join([f'{s//1000}K' for s in INPUT_SIZES])}\n")
        f.write(f"- 输出预算: {', '.join([f'{s//1000}K' for s in OUTPUT_SIZES])}\n")
        f.write(f"- 流式模式: {STREAM_MODES}\n")
        f.write(f"- 渠道: {', '.join(CHANNELS)}\n\n")
        
        f.write("## 测试结果汇总\n\n")
        
        success_count = sum(1 for r in results if r["success"])
        total_count = len(results)
        f.write(f"**总测试数**: {total_count}\n\n")
        f.write(f"**成功数**: {success_count} ({success_count/total_count*100:.1f}%)\n\n")
        f.write(f"**失败数**: {total_count - success_count}\n\n")
        
        f.write("## 详细结果\n\n")
        f.write("| 测试 | 状态 | TTFT | 总时长 | 错误 |\n")
        f.write("|------|------|------|--------|------|\n")
        
        for r in results:
            status = "✅" if r["success"] else "❌"
            ttft = f"{r['ttft_ms']}ms" if r['ttft_ms'] else "-"
            total = f"{r['total_time_ms']}ms" if r['total_time_ms'] else "-"
            error = r['error'][:50] if r['error'] else "-"
            f.write(f"| {r['test_name']} | {status} | {ttft} | {total} | {error} |\n")
        
        f.write("\n## 分析\n\n")
        
        # 按输入大小分组统计
        f.write("### 按输入长度统计\n\n")
        for input_size in INPUT_SIZES:
            subset = [r for r in results if r["input_size"] == input_size]
            success = sum(1 for r in subset if r["success"])
            total = len(subset)
            f.write(f"- **{input_size//1000}K**: {success}/{total} 成功\n")
        
        f.write("\n### 按输出预算统计\n\n")
        for output_size in OUTPUT_SIZES:
            subset = [r for r in results if r["output_size"] == output_size]
            success = sum(1 for r in subset if r["success"])
            total = len(subset)
            f.write(f"- **{output_size//1000}K**: {success}/{total} 成功\n")
        
        f.write("\n## 结论\n\n")
        f.write("（待填写：根据测试结果分析各渠道的实际承载能力）\n")
    
    print(f"\n=== 测试完成 ===")
    print(f"报告已保存: {report_file}")
    
    return report_file


if __name__ == "__main__":
    run_test_suite()
