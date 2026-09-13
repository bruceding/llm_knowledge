package agent

import (
	"io"
	"log"
	"os"
	"testing"
)

// TestMain 默认丢弃 log 输出。
//
// 原因:NewPiProtocol 在构造时会读部署的 web-search.json 并对两类问题告警
// (web 工具名重名会让 pi exit 1;扩展命令没关会让用户消息触发扩展代码)。
// 本包几乎每个用例都要构造 PiProtocol,而测试夹具用的是空临时目录 —— 也就是
// 「配置文件不存在」这个在生产里算 fail-open、在测试里算正常状态的输入,于是
// 每次 go test 都会往 stderr 打二十多行巨型告警,淹掉真正有用的输出。
//
// 只在需要断言日志的用例里用 captureAgentLog 显式接管(它会 SetOutput 到自己的
// buffer 并在 Cleanup 里还原成这里设的 io.Discard)。
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}
