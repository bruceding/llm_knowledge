#!/usr/bin/env python3
"""
使用 LLM 多模态能力将 PDF 页面转换为 Markdown
支持多种模型：Claude、Qwen 等
"""

import os
import sys
import base64
import json
import argparse
from pdf2image import convert_from_path
from PIL import Image
import io

def image_to_base64(image_path: str) -> str:
    """将图片转换为 base64"""
    with open(image_path, "rb") as f:
        return base64.standard_b64encode(f.read()).decode("utf-8")

def get_image_media_type(image_path: str) -> str:
    """获取图片的 MIME 类型"""
    ext = os.path.splitext(image_path)[1].lower()
    types = {
        ".png": "image/png",
        ".jpg": "image/jpeg",
        ".jpeg": "image/jpeg",
        ".gif": "image/gif",
        ".webp": "image/webp",
    }
    return types.get(ext, "image/png")

def process_with_claude(image_path: str, api_key: str, model: str = "claude-sonnet-4-6") -> str:
    """使用 Claude API 处理图片"""
    import anthropic

    client = anthropic.Anthropic(api_key=api_key)

    image_data = image_to_base64(image_path)
    media_type = get_image_media_type(image_path)

    message = client.messages.create(
        model=model,
        max_tokens=4096,
        messages=[
            {
                "role": "user",
                "content": [
                    {
                        "type": "image",
                        "source": {
                            "type": "base64",
                            "media_type": media_type,
                            "data": image_data,
                        },
                    },
                    {
                        "type": "text",
                        "text": """请分析这张 PDF 页面图片，将其转换为结构化的 Markdown 格式。

要求：
1. 保留文档的标题层级结构（使用 # ## ### 等）
2. 保留段落内容和换行
3. 如果有图片，用 ![描述](placeholder) 标记，并说明图片内容
4. 如果有表格，转换为 Markdown 表格格式
5. 如果有公式，使用 LaTeX 格式（$...$ 或 $$...$$）
6. 保持内容的逻辑顺序

请直接输出 Markdown 内容，不要添加额外解释。"""
                    }
                ],
            }
        ],
    )

    return message.content[0].text

def process_with_qwen(image_path: str, api_key: str, model: str = "qwen-vl-max") -> str:
    """使用阿里云 Qwen VL 模型处理图片"""
    import dashscope
    from dashscope import MultiModalConversation

    dashscope.api_key = api_key

    image_data = f"file://{image_path}"

    messages = [
        {
            "role": "user",
            "content": [
                {"image": image_data},
                {"text": """请分析这张 PDF 页面图片，将其转换为结构化的 Markdown 格式。

要求：
1. 保留文档的标题层级结构（使用 # ## ### 等）
2. 保留段落内容和换行
3. 如果有图片，用 ![描述](placeholder) 标记，并说明图片内容
4. 如果有表格，转换为 Markdown 表格格式
5. 如果有公式，使用 LaTeX 格式（$...$ 或 $$...$$）
6. 保持内容的逻辑顺序

请直接输出 Markdown 内容，不要添加额外解释。"""
                }
            ]
        }
    ]

    response = MultiModalConversation.call(model=model, messages=messages)

    if response.status_code == 200:
        return response.output.choices[0].message.content[0].text
    else:
        raise Exception(f"Qwen API error: {response.message}")

def pdf_to_markdown(pdf_path: str, output_path: str, api_key: str, provider: str = "claude", model: str = None, dpi: int = 150, pages: tuple = None):
    """将 PDF 转换为 Markdown"""

    # 获取 PDF 总页数
    from pdf2image.pdf2image import pdfinfo_from_path
    info = pdfinfo_from_path(pdf_path)
    total_pages = info.get("Pages", 0)

    if pages:
        start_page, end_page = pages
        end_page = min(end_page, total_pages)
    else:
        start_page = 1
        end_page = total_pages

    print(f"Processing PDF: {pdf_path}")
    print(f"Pages: {start_page} - {end_page} (total: {total_pages})")
    print(f"Provider: {provider}, Model: {model}")

    # 转换所有页面为图片
    all_pages = convert_from_path(pdf_path, first_page=start_page, last_page=end_page, dpi=dpi)

    # 临时目录保存图片
    temp_dir = "/tmp/pdf_llm_images"
    os.makedirs(temp_dir, exist_ok=True)

    markdown_content = []

    for i, page_image in enumerate(all_pages):
        page_num = start_page + i
        print(f"Processing page {page_num}...")

        # 保存图片
        image_path = os.path.join(temp_dir, f"page_{page_num}.png")
        page_image.save(image_path, "PNG")

        # 处理图片
        try:
            if provider == "claude":
                default_model = "claude-sonnet-4-6"
                content = process_with_claude(image_path, api_key, model or default_model)
            elif provider == "qwen":
                default_model = "qwen-vl-max"
                content = process_with_qwen(image_path, api_key, model or default_model)
            else:
                raise ValueError(f"Unknown provider: {provider}")

            markdown_content.append(f"\n---\n\n## Page {page_num}\n\n{content}")
            print(f"Page {page_num} done ({len(content)} chars)")
        except Exception as e:
            print(f"Error processing page {page_num}: {e}")
            markdown_content.append(f"\n---\n\n## Page {page_num}\n\n[Error: {e}]")

    # 写入输出文件
    final_content = "\n".join(markdown_content)
    with open(output_path, "w", encoding="utf-8") as f:
        f.write(final_content)

    print(f"Output saved to: {output_path}")

    return final_content

def main():
    parser = argparse.ArgumentParser(description="Convert PDF to Markdown using LLM")
    parser.add_argument("pdf_path", help="Path to PDF file")
    parser.add_argument("--output", "-o", default="output.md", help="Output markdown file")
    parser.add_argument("--provider", "-p", default="claude", choices=["claude", "qwen"], help="LLM provider")
    parser.add_argument("--model", "-m", help="Model name (default: claude-sonnet-4-6 or qwen-vl-max)")
    parser.add_argument("--api-key", "-k", help="API key (or set ANTHROPIC_API_KEY / DASHSCOPE_API_KEY env)")
    parser.add_argument("--dpi", "-d", type=int, default=150, help="Image DPI (default: 150)")
    parser.add_argument("--pages", nargs=2, type=int, metavar=("START", "END"), help="Page range to process")

    args = parser.parse_args()

    # 获取 API key
    api_key = args.api_key
    if not api_key:
        if args.provider == "claude":
            api_key = os.environ.get("ANTHROPIC_API_KEY")
        elif args.provider == "qwen":
            api_key = os.environ.get("DASHSCOPE_API_KEY")

    if not api_key:
        print(f"Error: No API key provided. Set {args.provider.upper()}_API_KEY or use --api-key")
        sys.exit(1)

    # 处理页面范围
    pages = tuple(args.pages) if args.pages else None

    # 转换
    pdf_to_markdown(
        pdf_path=args.pdf_path,
        output_path=args.output,
        api_key=api_key,
        provider=args.provider,
        model=args.model,
        dpi=args.dpi,
        pages=pages
    )

if __name__ == "__main__":
    main()