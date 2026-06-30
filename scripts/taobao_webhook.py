#!/usr/bin/env python3
"""
淘宝订单 webhook 处理器
接收千牛订单事件，自动生成 GLM-5.2 Token 并发送给买家
"""

from flask import Flask, request, jsonify
import requests
import json
import time

app = Flask(__name__)

ATMAPI_URL = "http://localhost:3002"

def generate_token(tier="starter"):
    """调用 atmApi 公开 API 生成 Token"""
    quota_map = {"starter": 500, "pro": 1000, "max": 1500}
    
    response = requests.post(
        f"{ATMAPI_URL}/api/generate-token",
        headers={"Content-Type": "application/json"},
        json={"tier": tier}
    )
    
    if response.status_code == 200:
        return response.json()
    else:
        raise Exception(f"生成 Token 失败: {response.text}")

def send_to_buyer(buyer_id, token_info):
    """
    通过千牛 SDK 发送消息给买家
    
    注意：这里需要使用千牛开放平台的 Python SDK
    暂时用 print 模拟，实际部署时替换为真实发送逻辑
    """
    message = f"""Hello~订单已处理

您的 GLM-5.2 API Key 已生成！

Token: {token_info['token']}
额度：{token_info['quota']}次/5小时（{token_info['tier'].upper()}版）
有效期：{token_info['expires_at']}

📖 使用说明：https://atmapi.aitomoney.online/help

谢谢！再来哦~"""
    
    # TODO: 使用千牛 SDK 发送消息
    # from alibabacloud_tea_openapi import models as open_api_models
    # client = create_client()
    # client.send_message(buyer_id, message)
    
    print(f"[发送给买家 {buyer_id}]")
    print(message)
    return True

@app.route('/webhook/taobao', methods=['POST'])
def handle_order():
    """处理淘宝订单 webhook"""
    try:
        data = request.json
        
        # 提取订单信息（根据千牛 webhook 格式调整）
        order_id = data.get('order_id')
        buyer_id = data.get('buyer_id')
        product_tier = data.get('tier', 'starter')  # 从商品 SKU 中获取品类
        
        print(f"收到订单: {order_id}, 买家: {buyer_id}, 品类: {product_tier}")
        
        # 1. 生成 Token
        token_info = generate_token(product_tier)
        print(f"✅ Token 生成成功: {token_info['token']}")
        
        # 2. 发送给买家
        send_to_buyer(buyer_id, token_info)
        
        return jsonify({"status": "success", "token": token_info['token']})
        
    except Exception as e:
        print(f"❌ 错误: {e}")
        return jsonify({"error": str(e)}), 500

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5003)
