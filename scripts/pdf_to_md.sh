#!/bin/bash
# PDF to Markdown using Claude CLI
# Usage: ./pdf_to_md.sh <pdf_path> [output_dir] [start_page] [end_page]

set -e

PDF_PATH="$1"
OUTPUT_DIR="${2:-output}"
START_PAGE="${3:-1}"
END_PAGE="${4:-all}"

if [ -z "$PDF_PATH" ]; then
    echo "Usage: $0 <pdf_path> [output_dir] [start_page] [end_page]"
    exit 1
fi

# 创建输出目录
mkdir -p "$OUTPUT_DIR"
mkdir -p "/tmp/pdf_pages"

# 获取 PDF 总页数
TOTAL_PAGES=$(pdfinfo "$PDF_PATH" | grep "Pages:" | awk '{print $2}')

if [ "$END_PAGE" = "all" ]; then
    END_PAGE=$TOTAL_PAGES
fi

echo "Processing PDF: $PDF_PATH"
echo "Pages: $START_PAGE - $END_PAGE (total: $TOTAL_PAGES)"
echo "Output: $OUTPUT_DIR/paper.md"

# 转换 PDF 页面为图片
echo "Converting PDF to images..."
pdftoppm -png -r 150 -f $START_PAGE -l $END_PAGE "$PDF_PATH" "/tmp/pdf_pages/page"

# 处理每一页
OUTPUT_FILE="$OUTPUT_DIR/paper.md"
echo "" > "$OUTPUT_FILE"

for i in $(seq $START_PAGE $END_PAGE); do
    # pdftoppm 输出格式: page-01.png, page-02.png 等
    PAGE_IMG="/tmp/pdf_pages/page-$(printf '%02d' $i).png"

    if [ -f "$PAGE_IMG" ]; then
        echo "Processing page $i..."

        # 使用 Claude 处理图片
        echo "" >> "$OUTPUT_FILE"
        echo "---" >> "$OUTPUT_FILE"
        echo "" >> "$OUTPUT_FILE"
        echo "## Page $i" >> "$OUTPUT_FILE"
        echo "" >> "$OUTPUT_FILE"

        claude --model sonnet --allowed-tools "Read" -p "读取图片 $PAGE_IMG，将其转换为 Markdown 格式。保留标题层级、段落结构、表格。如果有图片，用 ![描述](assets/img_$i.png) 标记。如果有公式，用 LaTeX 格式。直接输出内容，不要解释。" >> "$OUTPUT_FILE"

        echo "Page $i done"
    else
        echo "Warning: Page image not found for page $i"
    fi
done

echo "Done! Output saved to $OUTPUT_FILE"