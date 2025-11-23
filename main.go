package main

import (
	"fmt"
	"image/color"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/robinson/gos7"
)

const (
	defaultRack = 0
	defaultSlot = 1
)

type PLCBinaryViewer struct {
	client   gos7.Client
	handler  *gos7.TCPClientHandler
	running  bool
	stopChan chan bool
	mu       sync.Mutex
}

func NewPLCBinaryViewer() *PLCBinaryViewer {
	return &PLCBinaryViewer{
		stopChan: make(chan bool),
	}
}

func (p *PLCBinaryViewer) connectPLC(ip string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 如果已存在连接，先断开
	if p.client != nil {
		p.disconnectPLC()
		// 等待一小段时间确保连接完全断开
		time.Sleep(100 * time.Millisecond)
	}

	handler := gos7.NewTCPClientHandler(ip, defaultRack, defaultSlot)
	handler.Timeout = 5 * time.Second
	handler.IdleTimeout = 60 * time.Second
	handler.Logger = log.New(os.Stdout, "s7: ", log.LstdFlags)

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("连接PLC失败: %v", err)
	}

	p.handler = handler
	p.client = gos7.NewClient(handler)
	return nil
}

func (p *PLCBinaryViewer) disconnectPLC() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil {
		// 先断开客户端连接
		if p.handler != nil {
			p.handler.Close()
		}
		p.client = nil
		p.handler = nil
	}
}

func (p *PLCBinaryViewer) readVArea(startByte int, size int) ([]byte, error) {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()

	if client == nil {
		return nil, fmt.Errorf("PLC未连接")
	}

	buffer := make([]byte, size)

	// 尝试通过DB1访问V区（S7-200 Smart的V区映射到DB1）
	if err := client.AGReadDB(1, startByte, size, buffer); err != nil {
		// 如果DB1方式失败，尝试直接MB方式
		if err2 := client.AGReadMB(startByte, size, buffer); err2 != nil {
			return nil, fmt.Errorf("读取V区失败: %v, MB方式失败: %v", err, err2)
		}
	}
	return buffer, nil
}

// readOnce 单次读取数据，返回原始字节数据
func (p *PLCBinaryViewer) readOnce(startAddress int, length int) ([]byte, error) {
	// 根据长度计算需要读取的字节数
	bytesToRead := length
	if bytesToRead <= 0 {
		bytesToRead = 1
	}

	// 限制最大读取字节数（不超过32*20=640位，即80字节）
	maxBytes := 80 // 640位 / 8位/字节
	if bytesToRead > maxBytes {
		bytesToRead = maxBytes
	}

	// 直接读取字节数据
	data, err := p.readVArea(startAddress, bytesToRead)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// convertBytesTo16BitInts 将字节数组按16位分组转换为十进制数值
func convertBytesTo16BitInts(bytes []byte) []int {
	var result []int
	for i := 0; i < len(bytes); i += 2 {
		if i+1 < len(bytes) {
			// 16位无符号整数 (Big Endian)
			value := int(bytes[i])<<8 | int(bytes[i+1])
			result = append(result, value)
		} else {
			// 如果字节数为奇数，最后一个字节作为低8位，高8位为0
			value := int(bytes[i])
			result = append(result, value)
		}
	}
	return result
}

func (p *PLCBinaryViewer) startMonitoring(startAddress int, length int, updateFunc func([]bool)) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	stopChan := make(chan bool)
	p.stopChan = stopChan
	p.mu.Unlock()

	go func(startAddr int, len int, updateFn func([]bool)) {
		ticker := time.NewTicker(1000 * time.Millisecond) // 每1秒更新一次
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// 根据长度计算需要读取的字节数
				bytesToRead := len
				if bytesToRead <= 0 {
					bytesToRead = 1
				}

				// 限制最大读取字节数
				if bytesToRead > 4 {
					bytesToRead = 4
				}

				data, err := p.readVArea(startAddr, bytesToRead)
				if err != nil {
					log.Printf("读取数据失败: %v", err)
					continue
				}

				// 将字节数据转换为布尔数组（二进制位）
				totalBits := bytesToRead * 8
				bits := make([]bool, totalBits)
				for i, b := range data {
					for j := 0; j < 8; j++ {
						bitPos := i*8 + j
						bits[bitPos] = (b>>(7-j))&1 == 1
					}
				}

				if updateFn != nil {
					updateFn(bits)
				}
			}
		}
	}(startAddress, length, updateFunc)
}

func (p *PLCBinaryViewer) stopMonitoring() {
	p.mu.Lock()
	if p.running {
		close(p.stopChan)
		p.running = false
	}
	p.mu.Unlock()
}

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("S7-200 Smart V区二进制显示器 @Yuanxin E: wax_wane@qq.com ")
	myWindow.Resize(fyne.NewSize(900, 700))

	// 创建全局viewer实例
	var viewer *PLCBinaryViewer

	// 创建输入控件
	ipEntry := widget.NewEntry()
	ipEntry.SetText("192.168.1.11")

	addressEntry := widget.NewEntry()
	addressEntry.SetText("100") // 默认从V100开始

	lengthEntry := widget.NewEntry()
	lengthEntry.SetText("1") // 默认长度为1字节

	// 创建显示区域的容器
	displayContainer := container.NewVBox()

	// 创建寄存器内容显示文本框
	registerContentEntry := widget.NewMultiLineEntry()
	registerContentEntry.SetPlaceHolder("寄存器内容将以16位分组的十进制数值显示，用逗号分隔")
	registerContentEntry.Wrapping = fyne.TextWrapOff // 修正：使用正确的类型
	registerContentEntry.Resize(fyne.NewSize(850, 50))

	// 创建连接按钮
	connectButton := widget.NewButton("连接PLC", func() {
		ip := strings.TrimSpace(ipEntry.Text)
		if ip == "" {
			log.Println("请输入PLC IP地址")
			return
		}

		if viewer == nil {
			viewer = NewPLCBinaryViewer()
		}

		if err := viewer.connectPLC(ip); err != nil {
			log.Printf("连接失败: %v", err)
			return
		}

		log.Println("PLC连接成功!")
	})

	// 创建读取按钮（单次读取）
	monitorButton := widget.NewButton("读取数据", func() {
		if viewer == nil {
			log.Println("请先连接PLC")
			return
		}

		addressStr := strings.TrimSpace(addressEntry.Text)
		startAddress, err := strconv.Atoi(addressStr)
		if err != nil {
			log.Printf("无效的地址: %v", err)
			return
		}

		lengthStr := strings.TrimSpace(lengthEntry.Text)
		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			log.Printf("无效的长度: %v", err)
			return
		}

		// 设置最大读取字节数（不超过显示区域容量）
		const maxDisplayBytes = 80 // 32*20=640位 = 80字节
		bytesToRead := length
		if bytesToRead <= 0 {
			bytesToRead = 1
		}
		if bytesToRead > maxDisplayBytes {
			bytesToRead = maxDisplayBytes
		}

		// 创建固定大小的显示区域：32列 × 20行
		const (
			maxCols = 32
			maxRows = 20
		)

		// 创建一个垂直容器来存放所有行
		rowsContainer := container.NewVBox()

		// 创建一个全局方块引用数组，用于后续更新
		var squares [][]*canvas.Rectangle
		for row := 0; row < maxRows; row++ {
			rowSquares := make([]*canvas.Rectangle, maxCols)
			squares = append(squares, rowSquares)
		}

		// 创建32*20的网格
		for row := 0; row < maxRows; row++ {
			// 每行32个方块
			rowGrid := container.NewGridWithColumns(maxCols)

			for col := 0; col < maxCols; col++ {
				// 创建灰色方块（初始状态）
				square := canvas.NewRectangle(color.RGBA{R: 128, G: 128, B: 128, A: 255}) // 灰色表示未使用
				square.SetMinSize(fyne.NewSize(25, 25))
				squares[row][col] = square
				rowGrid.Add(square)
			}

			rowsContainer.Add(rowGrid)
		}

		displayContainer.Objects = []fyne.CanvasObject{rowsContainer}
		displayContainer.Refresh()

		// 单次读取数据
		dataBytes, err := viewer.readOnce(startAddress, bytesToRead)
		if err != nil {
			log.Printf("读取数据失败: %v", err)
			return
		}

		// 将字节数据转换为16位十进制数值
		decValues := convertBytesTo16BitInts(dataBytes)
		var decStr []string
		for _, val := range decValues {
			decStr = append(decStr, strconv.Itoa(val))
		}
		registerContentEntry.SetText(strings.Join(decStr, ", "))

		// 将字节数据转换为二进制位并填充到32*20的网格中
		for i := 0; i < len(dataBytes); i++ {
			for j := 0; j < 8; j++ {
				// 计算位的索引
				bitIndex := i*8 + j
				// 计算在网格中的位置
				row := bitIndex / maxCols
				col := bitIndex % maxCols

				// 检查是否在显示区域内
				if row < maxRows && col < maxCols {
					square := squares[row][col]
					// 提取当前位的值（从高位到低位）
					bitValue := (dataBytes[i] >> (7 - j)) & 1
					if bitValue == 1 {
						square.FillColor = color.RGBA{R: 0, G: 255, B: 0, A: 255} // 绿色表示1
					} else {
						square.FillColor = color.RGBA{R: 128, G: 128, B: 128, A: 255} // 灰色表示0
					}
					square.Refresh()
				}
			}
		}

		// 对于未使用的网格部分，保持灰色状态
		totalDataBits := len(dataBytes) * 8
		for bitIndex := totalDataBits; bitIndex < maxRows*maxCols; bitIndex++ {
			row := bitIndex / maxCols
			col := bitIndex % maxCols
			if row < maxRows && col < maxCols {
				square := squares[row][col]
				square.FillColor = color.RGBA{R: 128, G: 128, B: 128, A: 255} // 灰色表示未使用
				square.Refresh()
			}
		}
	})

	// 断开连接按钮
	disconnectButton := widget.NewButton("断开连接", func() {
		if viewer != nil {
			viewer.disconnectPLC()
			log.Println("PLC已断开连接")
		}
	})

	// 清除显示按钮
	stopButton := widget.NewButton("清除显示", func() {
		// 重新创建空的显示区域
		displayContainer.Objects = nil
		displayContainer.Refresh()
		// 清除寄存器内容显示
		registerContentEntry.SetText("")
	})

	// 布局
	inputForm := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("PLC IP地址:", ipEntry),
			widget.NewFormItem("起始地址 (V区):", addressEntry),
			widget.NewFormItem("寄存器长度 (字节):", lengthEntry),
		),
		container.NewHBox(
			connectButton,
			disconnectButton,
			monitorButton,
			stopButton,
		),
	)

	// 将寄存器内容显示放在输入表单和显示区域之间
	content := container.NewBorder(
		container.NewVBox(
			inputForm,
			widget.NewLabel("寄存器内容 (16位十进制数值):"),
			registerContentEntry,
		),
		nil, nil, nil,
		container.NewVScroll(displayContainer))

	myWindow.SetContent(content)
	myWindow.ShowAndRun()
}
