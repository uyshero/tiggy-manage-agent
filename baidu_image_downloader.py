#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
百度图片搜索下载脚本
使用方法: python baidu_image_downloader.py "搜索关键词" [下载数量]
"""

import os
import json
import time
import random
import requests
import argparse
from urllib.parse import quote


class BaiduImageDownloader:
    def __init__(self):
        self.session = requests.Session()
        self.headers = {
            'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
            'Referer': 'https://image.baidu.com/',
            'Accept': 'application/json, text/javascript, */*; q=0.01',
            'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
            'Connection': 'keep-alive',
            'sec-ch-ua': '"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"',
            'sec-ch-ua-mobile': '?0',
            'sec-ch-ua-platform': '"macOS"',
            'Sec-Fetch-Dest': 'empty',
            'Sec-Fetch-Mode': 'cors',
            'Sec-Fetch-Site': 'same-origin',
            'X-Requested-With': 'XMLHttpRequest',
        }
        self.base_url = 'https://image.baidu.com/search/acjson'
        # 先访问首页获取cookies
        self._init_session()
    
    def _init_session(self):
        """初始化会话，获取cookies"""
        home_headers = self.headers.copy()
        home_headers['Accept'] = 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8'
        home_headers['Sec-Fetch-Dest'] = 'document'
        home_headers['Sec-Fetch-Mode'] = 'navigate'
        home_headers['Sec-Fetch-Site'] = 'none'
        home_headers['Sec-Fetch-User'] = '?1'
        home_headers['Upgrade-Insecure-Requests'] = '1'
        try:
            self.session.get('https://image.baidu.com/', headers=home_headers, timeout=15)
            time.sleep(0.5)
        except:
            pass

    def get_image_urls(self, keyword, max_count=30):
        """
        获取图片URL列表
        :param keyword: 搜索关键词
        :param max_count: 最大下载数量
        :return: 图片URL列表
        """
        images = []
        page = 0
        empty_count = 0  # 连续空页计数器
        
        while len(images) < max_count and empty_count < 3:  # 连续3次空页则停止
            pn = page * 30
            params = {
                'tn': 'resultjson_com',
                'ipn': 'rj',
                'ct': '201326592',
                'fp': 'result',
                'word': quote(keyword),
                'queryWord': quote(keyword),
                'pn': pn,
                'rn': 30,
                'ie': 'utf-8',
                'oe': 'utf-8',
                'lm': '-1',
                'face': '0',
            }
            
            try:
                print(f"正在获取第 {page + 1} 页图片... (已收集 {len(images)}/{max_count})")
                response = self.session.get(
                    self.base_url, 
                    params=params, 
                    headers=self.headers, 
                    timeout=15
                )
                
                # 尝试解析JSON
                try:
                    data = response.json()
                except:
                    # 处理可能的JSONP格式
                    text = response.text
                    if text.startswith('('):
                        text = text[1:]
                    if text.endswith(');'):
                        text = text[:-2]
                    elif text.endswith(')'):
                        text = text[:-1]
                    data = json.loads(text)
                
                page_images_count = 0
                if 'data' in data:
                    for item in data['data']:
                        if 'thumbURL' in item:
                            # 优先使用middleURL，如果没有则使用thumbURL
                            img_url = item.get('middleURL') or item.get('thumbURL')
                            if img_url and img_url not in [img['url'] for img in images]:
                                images.append({
                                    'url': img_url,
                                    'title': item.get('fromPageTitle', '')
                                })
                                page_images_count += 1
                                if len(images) >= max_count:
                                    return images
                
                if page_images_count == 0:
                    empty_count += 1
                else:
                    empty_count = 0  # 重置空页计数器
                
                # 随机延迟，避免反爬
                time.sleep(random.uniform(0.5, 1.5))
                
            except Exception as e:
                print(f"获取第 {page + 1} 页失败: {str(e)}")
                empty_count += 1
            
            page += 1
        
        return images[:max_count]

    def download_image(self, img_info, save_dir):
        """
        下载单张图片
        :param img_info: 图片信息字典
        :param save_dir: 保存目录
        :return: 是否成功
        """
        url = img_info['url']
        if not url:
            return False
            
        try:
            # 获取图片扩展名
            ext = 'jpg'  # 默认扩展名
            if '.' in url:
                url_ext = url.split('.')[-1].split('?')[0].split('&')[0].lower()
                if url_ext in ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp']:
                    ext = url_ext
            
            # 生成文件名
            filename = f"img_{int(time.time() * 1000)}_{random.randint(1000, 9999)}.{ext}"
            filepath = os.path.join(save_dir, filename)
            
            # 下载图片
            response = self.session.get(url, headers=self.headers, timeout=20, stream=True)
            response.raise_for_status()
            
            with open(filepath, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    f.write(chunk)
            
            print(f"✓ 下载成功: {filename}")
            return True
            
        except Exception as e:
            print(f"✗ 下载失败: {str(e)[:50]}...")
            return False

    def download(self, keyword, count=30, save_dir=None):
        """
        主下载函数
        :param keyword: 搜索关键词
        :param count: 下载数量
        :param save_dir: 保存目录
        """
        # 设置保存目录
        if save_dir is None:
            save_dir = os.path.join('baidu_images', keyword.replace(' ', '_'))
        
        if not os.path.exists(save_dir):
            os.makedirs(save_dir)
        
        print(f"\n{'='*50}")
        print(f"搜索关键词: {keyword}")
        print(f"计划下载: {count} 张")
        print(f"保存目录: {save_dir}")
        print(f"{'='*50}\n")
        
        # 获取图片URL列表
        print("正在搜索图片...")
        images = self.get_image_urls(keyword, count)
        
        if not images:
            print("未找到任何图片！")
            return
        
        print(f"\n找到 {len(images)} 张图片，开始下载...\n")
        
        # 下载图片
        success_count = 0
        for i, img_info in enumerate(images, 1):
            print(f"[{i}/{len(images)}] ", end='')
            if self.download_image(img_info, save_dir):
                success_count += 1
            # 下载间隔
            time.sleep(random.uniform(0.2, 0.8))
        
        print(f"\n{'='*50}")
        print(f"下载完成！成功: {success_count}/{len(images)}")
        print(f"图片保存在: {os.path.abspath(save_dir)}")
        print(f"{'='*50}\n")


def main():
    parser = argparse.ArgumentParser(description='百度图片搜索下载工具')
    parser.add_argument('keyword', help='搜索关键词')
    parser.add_argument('-n', '--number', type=int, default=30, help='下载数量（默认30）')
    parser.add_argument('-d', '--dir', help='保存目录（默认为 baidu_images/关键词）')
    
    args = parser.parse_args()
    
    downloader = BaiduImageDownloader()
    downloader.download(args.keyword, args.number, args.dir)


if __name__ == '__main__':
    # 命令行模式
    import sys
    if len(sys.argv) > 1:
        main()
    else:
        # 交互式模式
        print("百度图片搜索下载工具")
        print("="*40)
        keyword = input("请输入搜索关键词: ").strip()
        if not keyword:
            print("关键词不能为空！")
            sys.exit(1)
        
        count_input = input("请输入下载数量（默认30）: ").strip()
        count = int(count_input) if count_input.isdigit() else 30
        
        downloader = BaiduImageDownloader()
        downloader.download(keyword, count)
