#!/usr/bin/env python3
"""
GLM-5.2 72小时烤机测试脚本
持续发送请求，验证系统稳定性
"""

import subprocess
import requests
import time
import random
import json
from datetime import datetime

# 配置
API_URL = "http://localhost:3300/v1/chat/completions"
LOG_FILE = "/tmp/glm52_bake_test.log"
TOKEN_ID = 385

def get_api_key():
    """从数据库获取 API key"""
    try:
        result = subprocess.check_output(
            ['mysql', '-h', '127.0.0.1', '-u', 'atmapi', '-patmapi2026', 'atmapi', '-N', '-e',
             f"SELECT `key` FROM tokens WHERE id={TOKEN_ID};"],
            stderr=subprocess.STDOUT, timeout=10
        )
        key = result.decode().strip()
        if not key:
            raise ValueError(f"Token ID {TOKEN_ID} 不存在")
        return key
    except Exception as e:
        log(f"❌ 获取 API Key 失败: {e}")
        raise

def get_balance():
    """获取当前余额，同时检查是否无限额度"""
    try:
        # 先检查 unlimited_quota
        result = subprocess.check_output(
            ['mysql', '-h', '127.0.0.1', '-u', 'atmapi', '-patmapi2026', 'atmapi', '-N', '-e',
             f"SELECT unlimited_quota FROM tokens WHERE id={TOKEN_ID};"],
            stderr=subprocess.STDOUT, timeout=10
        )
        if result.decode().strip() == '1':
            return 0, 999999  # 无限额度，返回大数
    except:
        pass

    try:
        result = subprocess.check_output(
            ['mysql', '-h', '127.0.0.1', '-u', 'atmapi', '-patmapi2026', 'atmapi', '-N', '-e',
             f"SELECT used_points, total_points FROM glm_points_ledger WHERE token_id={TOKEN_ID};"],
            stderr=subprocess.STDOUT, timeout=10
        )
        parts = result.decode().strip().split('\t')
        if len(parts) < 2:
            raise ValueError(f"查询结果异常: {result.decode().strip()}")
        return int(parts[0]), int(parts[1])
    except Exception as e:
        log(f"⚠️ 获取余额失败: {e}，使用默认值")
        return 0, 999999  # 容错：如果查不到余额，继续跑

def log(msg):
    """记录日志"""
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    log_msg = f"[{timestamp}] {msg}"
    print(log_msg)
    with open(LOG_FILE, 'a', encoding='utf-8') as f:
        f.write(log_msg + '\n')

def send_request(api_key, test_type):
    """发送测试请求"""
    headers = {
        'Content-Type': 'application/json',
        'Authorization': f'Bearer {api_key}'
    }

    if test_type == 'simple':
        # ⚠️ GLM-5.2 是推理模型，max_tokens 必须 >= 2000
        payload = {
            'model': 'glm-5.2',
            'messages': [{'role': 'user', 'content': '你好'}],
            'max_tokens': 2000,
            'temperature': 0
        }
    elif test_type == 'cache':
        # 缓存测试：相同内容
        # 缓存测试：相同内容
        payload = {
            'model': 'glm-5.2',
            'messages': [{'role': 'user', 'content': '1+1等于几？只回答数字'}],
            'max_tokens': 2000,
            'temperature': 0
        }
    elif test_type == 'medium':
        payload = {
            'model': 'glm-5.2',
            'messages': [{'role': 'user', 'content': '用一句话解释什么是人工智能'}],
            'max_tokens': 2000,
            'temperature': 0.7
        }
    else:  # long
        payload = {
            'model': 'glm-5.2',
            'messages': [{'role': 'user', 'content': '请详细介绍机器学习的主要算法，包括监督学习、无监督学习和强化学习'}],
            'max_tokens': 2000,
            'temperature': 0.7
        }

    start_time = time.time()
    max_retries = 2
    for attempt in range(max_retries + 1):
        try:
            # long 类型需要更长 timeout（GLM-5.2 推理模型生成慢）
            timeout = 120 if test_type == 'long' else 30
            response = requests.post(API_URL, headers=headers, json=payload, timeout=timeout)
            duration = int((time.time() - start_time) * 1000)

            result = {
                'status': response.status_code,
                'duration': duration,
                'cache_hit': response.headers.get('X-Cache-Hit', 'false'),
                'singleflight': response.headers.get('X-Singleflight-Shared', 'false')
            }

            if response.status_code == 200:
                data = response.json()
                result['content'] = data.get('choices', [{}])[0].get('message', {}).get('content', '')[:50]
            else:
                result['error'] = response.text[:200]

            # 如果失败且有重试机会，等 2 秒重试
            if response.status_code != 200 and attempt < max_retries:
                time.sleep(2)
                continue

            return result
        except Exception as e:
            if attempt < max_retries:
                time.sleep(2)
                continue
            duration = int((time.time() - start_time) * 1000)
            return {'status': 0, 'error': str(e), 'duration': duration, 'cache_hit': 'false'}

def main():
    """主循环"""
    log("=" * 60)
    log("GLM-5.2 72小时烤机测试启动")
    log("=" * 60)

    api_key = get_api_key()
    log(f"API Key: {api_key[:15]}...")

    # 统计
    stats = {
        'total': 0,
        'success': 0,
        'failed': 0,
        'cache_hit': 0,
        'start_time': time.time()
    }

    test_types = ['simple', 'cache', 'cache', 'medium', 'long']  # 缓存测试多一些

    try:
        # 初始化余额变量
        remaining = 0
        total = 0
        usage_rate = 0.0
        
        while True:
            # 检查余额（每 50 次检查一次，减少 DB 压力）
            if stats['total'] % 50 == 0:
                used, total = get_balance()
                remaining = total - used
                usage_rate = (used / total * 100) if total > 0 else 0

                if remaining < 100:
                    log(f"⚠️  余额不足: {remaining} 点，停止测试")
                    break
            
            # 初始化变量（避免首次循环时未定义）
            if 'remaining' not in locals():
                remaining = 0
                total = 0
                usage_rate = 0

            # 选择测试类型
            test_type = random.choice(test_types)

            # 发送请求
            result = send_request(api_key, test_type)
            stats['total'] += 1

            # 记录每次请求
            status_emoji = "✅" if result['status'] == 200 else "❌"
            cache_emoji = "🔥" if result.get('cache_hit') == 'true' else "  "
            log(f"{status_emoji} #{stats['total']+1:04d} [{test_type:6s}] {result['status']} {result['duration']:5d}ms {cache_emoji} cache={result.get('cache_hit','false')}")
            
            if result['status'] != 200:
                log(f"   └─ 错误: {result.get('error', 'unknown')[:100]}")

            if result['status'] == 200:
                stats['success'] += 1
                if result.get('cache_hit') == 'true':
                    stats['cache_hit'] += 1
            else:
                stats['failed'] += 1

            # 每10次请求输出一次统计
            if stats['total'] % 10 == 0:
                elapsed = time.time() - stats['start_time']
                success_rate = (stats['success'] / stats['total'] * 100) if stats['total'] > 0 else 0
                cache_rate = (stats['cache_hit'] / stats['success'] * 100) if stats['success'] > 0 else 0

                log(f"📊 统计: 总请求={stats['total']}, 成功={stats['success']}, "
                    f"失败={stats['failed']}, 成功率={success_rate:.1f}%, "
                    f"缓存命中={stats['cache_hit']}, 缓存率={cache_rate:.1f}%, "
                    f"余额={remaining}/{total} ({usage_rate:.1f}%), "
                    f"运行时间={elapsed/60:.1f}分钟")

            # 随机等待 1-5 秒
            wait_time = random.uniform(1, 5)
            time.sleep(wait_time)

    except KeyboardInterrupt:
        log("\n⚠️  测试被中断")
    except Exception as e:
        log(f"\n❌ 测试异常: {e}")

    # 最终统计
    elapsed = time.time() - stats['start_time']
    success_rate = (stats['success'] / stats['total'] * 100) if stats['total'] > 0 else 0
    cache_rate = (stats['cache_hit'] / stats['success'] * 100) if stats['success'] > 0 else 0

    log("\n" + "=" * 60)
    log("🏁 烤机测试结束")
    log("=" * 60)
    log(f"总请求数: {stats['total']}")
    log(f"成功请求: {stats['success']}")
    log(f"失败请求: {stats['failed']}")
    log(f"成功率: {success_rate:.1f}%")
    log(f"缓存命中: {stats['cache_hit']}")
    log(f"缓存命中率: {cache_rate:.1f}%")
    log(f"运行时间: {elapsed/3600:.2f} 小时")
    log("=" * 60)

if __name__ == '__main__':
    main()
