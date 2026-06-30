#!/usr/bin/env python3
"""
淘宝自动发货 - GLM-5.2 Token 自动生成脚本
监听千牛订单 webhook，付款后自动创建 MAS Token 并通过旺旺发送
"""

import requests
import json
import sys
from datetime import datetime, timedelta

# atmApi 配置
ATMAPI_URL = "http://localhost:3002"
ADMIN_TOKEN = "sk-admin-your-token-here"  # 需要管理员 Token

def create_mas_token(tier="starter"):
    """
    根据品类创建 MAS Token
    
    tier: starter(500次), pro(1000次), max(1500次)
    """
    quota_map = {
        "starter": 500,
        "pro": 1000,
        "max": 1500
    }
    
    headers = {
        "Authorization": f"Bearer {ADMIN_TOKEN}",
        "Content-Type": "application/json"
    }
    
    payload = {
        "tier": tier,
        "quota": quota_map.get(tier, 500),
        "expires_in_days": 30  # 30天有效期
    }
    
    response = requests.post(
        f"{ATMAPI_URL}/v1/mas/tokens",
        headers=headers,
        json=payload
    )
    
    if response.status_code == 200:
        data = response.json()
        return {
            "token": data.get("token"),
            "tier": tier,
            "quota": quota_map.get(tier, 500)
        }
    else:
        raise Exception(f"创建 Token 失败: {response.text}")

def send_to_buyer(order_id, token_info):
    """
    通过千牛 API 发送 Token 给买家
    
    这里需要根据千牛的 SDK 或 API 实现
    暂时返回消息内容，实际部署时替换为真实的发送逻辑
    """
    message = f"""Hello~订单：{order_id}

您的 GLM-5.2 API Key 已生成！

Token: {token_info['token']}
额度：{token_info['quota']}次/5小时（{token_info['tier'].upper()}版）
有效期：30天

📖 使用说明：https://atmapi.aitomoney.online/help

谢谢！再来哦~"""
    
    print(message)
    return message

if __name__ == "__main__":
    # 从命令行参数获取订单信息
    if len(sys.argv) < 3:
        print("用法: python3 auto_generate_token.py <order_id> <tier>")
        print("tier: starter/pro/max")
        sys.exit(1)
    
    order_id = sys.argv[1]
    tier = sys.argv[2]
    
    try:
        # 1. 创建 Token
        token_info = create_mas_token(tier)
        print(f"✅ Token 创建成功: {token_info['token']}")
        
        # 2. 发送给买家
        send_to_buyer(order_id, token_info)
        
    except Exception as e:
        print(f"❌ 错误: {e}")
        sys.exit(1)
