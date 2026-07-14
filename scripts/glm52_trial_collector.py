#!/usr/bin/env python3
"""
GLM-5.2 试运营样本收集脚本
收集真实用户的使用数据，用于成本利润复盘
"""

import subprocess
import json
from datetime import datetime, timedelta
import MySQLdb

# 配置
DB_CONFIG = {
    'host': '127.0.0.1',
    'user': 'atmapi',
    'passwd': 'atmapi2026',
    'db': 'atmapi'
}

REPORT_FILE = "/tmp/glm52_trial_report.json"

def query_db(sql, params=None):
    """执行数据库查询"""
    conn = MySQLdb.connect(**DB_CONFIG)
    cursor = conn.cursor(MySQLdb.cursors.DictCursor)
    cursor.execute(sql, params or ())
    results = cursor.fetchall()
    conn.close()
    return results

def collect_trial_data(days=7):
    """收集试运营数据"""
    print(f"📊 收集最近 {days} 天的试运营数据...")
    
    # 1. GLM-5.2 套餐用户
    tokens = query_db("""
        SELECT id, name, plan_name, created_at
        FROM tokens
        WHERE plan_name IN ('glm-basic', 'glm-standard', 'glm-pro')
        AND status = 'active'
    """)
    
    print(f"  找到 {len(tokens)} 个 GLM-5.2 套餐用户")
    
    # 2. 每个用户的请求统计
    user_stats = []
    for token in tokens:
        token_id = token['id']
        
        # 请求统计
        requests = query_db(f"""
            SELECT 
                COUNT(*) as total_requests,
                SUM(CASE WHEN status_code = 200 THEN 1 ELSE 0 END) as success_requests,
                SUM(CASE WHEN local_response_cache_hit = 1 THEN 1 ELSE 0 END) as cache_hits,
                SUM(input_tokens) as total_input_tokens,
                SUM(output_tokens) as total_output_tokens,
                SUM(estimated_cost) as total_cost,
                AVG(duration_ms) as avg_duration
            FROM usage_logs
            WHERE token_id = {token_id}
            AND created_at > DATE_SUB(NOW(), INTERVAL {days} DAY)
        """)[0]
        
        # 点数消耗
        points = query_db(f"""
            SELECT used_points, total_points
            FROM glm_points_ledger
            WHERE token_id = {token_id}
        """)
        
        points_data = points[0] if points else {'used_points': 0, 'total_points': 0}
        
        # 计算关键指标
        total_requests = requests['total_requests'] or 0
        success_requests = requests['success_requests'] or 0
        cache_hits = requests['cache_hits'] or 0
        
        success_rate = (success_requests / total_requests * 100) if total_requests > 0 else 0
        cache_rate = (cache_hits / success_requests * 100) if success_requests > 0 else 0
        points_usage_rate = (points_data['used_points'] / points_data['total_points'] * 100) if points_data['total_points'] > 0 else 0
        
        # 实际成本 vs 标准结算价
        actual_cost = requests['total_cost'] or 0
        standard_cost = points_data['used_points'] * 0.01  # 100点=¥1
        
        # 利润 = 标准结算价 - 实际成本
        profit = standard_cost - actual_cost
        profit_rate = (profit / standard_cost * 100) if standard_cost > 0 else 0
        
        user_stat = {
            'token_id': token_id,
            'token_name': token['name'],
            'plan_name': token['plan_name'],
            'created_at': str(token['created_at']),
            'total_requests': total_requests,
            'success_requests': success_requests,
            'success_rate': round(success_rate, 2),
            'cache_hits': cache_hits,
            'cache_rate': round(cache_rate, 2),
            'total_input_tokens': requests['total_input_tokens'] or 0,
            'total_output_tokens': requests['total_output_tokens'] or 0,
            'points_used': points_data['used_points'],
            'points_total': points_data['total_points'],
            'points_usage_rate': round(points_usage_rate, 2),
            'actual_cost_cny': round(actual_cost, 4),
            'standard_cost_cny': round(standard_cost, 4),
            'profit_cny': round(profit, 4),
            'profit_rate': round(profit_rate, 2),
            'avg_duration_ms': round(requests['avg_duration'] or 0, 2)
        }
        
        user_stats.append(user_stat)
        print(f"  用户 {token['name']}: {total_requests} 请求, 成功率 {success_rate:.1f}%, 缓存率 {cache_rate:.1f}%, 利润率 {profit_rate:.1f}%")
    
    # 3. 汇总统计
    total_users = len(user_stats)
    total_requests = sum(u['total_requests'] for u in user_stats)
    total_actual_cost = sum(u['actual_cost_cny'] for u in user_stats)
    total_standard_cost = sum(u['standard_cost_cny'] for u in user_stats)
    total_profit = sum(u['profit_cny'] for u in user_stats)
    
    avg_success_rate = sum(u['success_rate'] for u in user_stats) / total_users if total_users > 0 else 0
    avg_cache_rate = sum(u['cache_rate'] for u in user_stats) / total_users if total_users > 0 else 0
    avg_profit_rate = (total_profit / total_standard_cost * 100) if total_standard_cost > 0 else 0
    
    summary = {
        'report_time': datetime.now().isoformat(),
        'period_days': days,
        'total_users': total_users,
        'total_requests': total_requests,
        'avg_success_rate': round(avg_success_rate, 2),
        'avg_cache_rate': round(avg_cache_rate, 2),
        'total_actual_cost_cny': round(total_actual_cost, 4),
        'total_standard_cost_cny': round(total_standard_cost, 4),
        'total_profit_cny': round(total_profit, 4),
        'avg_profit_rate': round(avg_profit_rate, 2),
        'user_details': user_stats
    }
    
    # 4. 保存报告
    with open(REPORT_FILE, 'w', encoding='utf-8') as f:
        json.dump(summary, f, ensure_ascii=False, indent=2)
    
    print(f"\n📊 试运营报告已生成: {REPORT_FILE}")
    print(f"  总用户数: {total_users}")
    print(f"  总请求数: {total_requests}")
    print(f"  平均成功率: {avg_success_rate:.1f}%")
    print(f"  平均缓存率: {avg_cache_rate:.1f}%")
    print(f"  总实际成本: ¥{total_actual_cost:.4f}")
    print(f"  总标准结算: ¥{total_standard_cost:.4f}")
    print(f"  总利润: ¥{total_profit:.4f}")
    print(f"  平均利润率: {avg_profit_rate:.1f}%")
    
    return summary

def generate_feishu_report(summary):
    """生成飞书文档报告"""
    print("\n📝 生成飞书文档报告...")
    
    report_md = f"""# GLM-5.2 试运营成本利润复盘报告

**报告时间**: {summary['report_time']}
**统计周期**: 最近 {summary['period_days']} 天

---

## 📊 核心指标

| 指标 | 数值 |
|------|------|
| 总用户数 | {summary['total_users']} |
| 总请求数 | {summary['total_requests']} |
| 平均成功率 | {summary['avg_success_rate']:.1f}% |
| 平均缓存命中率 | {summary['avg_cache_rate']:.1f}% |

---

## 💰 成本利润分析

| 项目 | 金额 (¥) |
|------|----------|
| 总实际成本 | {summary['total_actual_cost_cny']:.4f} |
| 总标准结算 | {summary['total_standard_cost_cny']:.4f} |
| **总利润** | **{summary['total_profit_cny']:.4f}** |
| **平均利润率** | **{summary['avg_profit_rate']:.1f}%** |

---

## 👥 用户详情

| 用户 | 套餐 | 请求数 | 成功率 | 缓存率 | 点数使用率 | 实际成本 | 标准结算 | 利润 | 利润率 |
|------|------|--------|--------|--------|------------|----------|----------|------|--------|
"""
    
    for user in summary['user_details']:
        report_md += f"| {user['token_name'][:20]} | {user['plan_name']} | {user['total_requests']} | {user['success_rate']:.1f}% | {user['cache_rate']:.1f}% | {user['points_usage_rate']:.1f}% | ¥{user['actual_cost_cny']:.4f} | ¥{user['standard_cost_cny']:.4f} | ¥{user['profit_cny']:.4f} | {user['profit_rate']:.1f}% |\n"
    
    report_md += f"""
---

## 🔍 关键发现

1. **缓存优化效果**: 平均缓存命中率 {summary['avg_cache_rate']:.1f}%，有效降低上游成本
2. **利润率分析**: 平均利润率 {summary['avg_profit_rate']:.1f}%，{'健康' if summary['avg_profit_rate'] > 10 else '偏低，需要优化'}
3. **用户活跃度': 平均每用户 {summary['total_requests'] / summary['total_users']:.0f} 次请求

---

## 💡 优化建议

1. **继续优化缓存策略**: 提高缓存命中率，降低上游成本
2. **监控重度用户**: 点数使用率 > 85% 的用户需要关注
3. **定价微调**: 根据实际利润率调整套餐价格

---

*报告由 GLM-5.2 试运营样本收集脚本自动生成*
"""
    
    report_file = "/tmp/glm52_trial_report.md"
    with open(report_file, 'w', encoding='utf-8') as f:
        f.write(report_md)
    
    print(f"  报告已保存: {report_file}")
    return report_file

if __name__ == '__main__':
    import sys
    days = int(sys.argv[1]) if len(sys.argv) > 1 else 7
    
    summary = collect_trial_data(days)
    report_file = generate_feishu_report(summary)
    
    print(f"\n✅ 试运营样本收集完成")
    print(f"  JSON 报告: {REPORT_FILE}")
    print(f"  Markdown 报告: {report_file}")
