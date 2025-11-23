# s7-200smart-v-binary-viewer
电脑上利用S7指令直接查看西门子S7-200 smart的变量存储区的值

go mod init plc-binary-viewer

go get fyne.io/fyne/v2@latest

go get github.com/robinson/gos7


go build -ldflags="-s -w" -o S7-200-smart变量区查看器.exe main.go


参数说明：
- -ldflags="-s -w": 减小可执行文件大小，移除调试信息
- 
- -o plc_binary_viewer.exe: 指定输出文件名
![show](https://github.com/user-attachments/assets/417fdc6d-8122-4742-9e10-ba9782d37c4b)
