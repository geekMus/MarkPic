// Package brand 收产品名这类「会出现在用户眼前」的字面量。
//
// 它和 module path 不是一回事：module 跟着仓库走，名字跟着产品走。改名时只动这里
// 和界面里的字标，命令行的用法示例由 usageText 拼出来，不用逐处手改。
package brand

const (
	// Name 是产品名：窗口标题、任务栏、关于弹窗、命令行抬头都用它。
	Name = "MarkPic"

	// Slug 是小写形式：只用在目录名、文件名这类不适合空格与大写的地方。
	Slug = "markpic"

	// AppUserModelID 决定 Windows 把窗口归到哪个任务栏身份下。
	// 改名后旧的身份不再复用——钉过任务栏的用户需要取消固定再重钉一次。
	AppUserModelID = "geekMus.markpic"
)
