package better_chunk

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
)

// BmpHead 定义了位图的头部
var BmpHead = []byte{66, 77, 66, 0, 0, 0, 0, 0, 0, 0, 54, 0, 0, 0, 40, 0, 0, 0, 4, 0, 0, 0, 1, 0, 0, 0, 1, 0, 24, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// buildBmpHead 根据宽和高构建位图头部
func buildBmpHead(width, height int64) []byte {
	resBmpHead := make([]byte, len(BmpHead))
	copy(resBmpHead, BmpHead)

	bfSize := intToByteArray(54 + width*height*3) // 从0开始计算  第2—5字节
	copy(resBmpHead[2:6], bfSize)

	biWidth := intToByteArray(width) // 位图的宽（18-21字节）
	copy(resBmpHead[18:22], biWidth)

	biHeight := intToByteArray(height) // 位图的高（22-25字节）
	copy(resBmpHead[22:26], biHeight)

	biSizeImage := intToByteArray(width * height * 3) // 实际位图图像的大小（34-37字节）
	copy(resBmpHead[34:38], biSizeImage)

	return resBmpHead
}

// intToByteArray 将整数转换为4字节的字节数组
func intToByteArray(value int64) []byte {
	byteArray := make([]byte, 4)
	binary.LittleEndian.PutUint32(byteArray, uint32(value))
	return byteArray
}

// getBmpSize 根据数据大小计算位图的宽和高
func getBmpSize(dataSize int64) (int64, int64) {
	sizeSqrt := int64(math.Ceil(math.Sqrt((float64(dataSize) + 4) / 3.0)))
	if sizeSqrt < 4 {
		sizeSqrt = 4
	}
	width := sizeSqrt / 4 * 4
	height := int64(math.Ceil((float64(dataSize) + 4) / 3.0 / float64(width)))
	return width, height
}

func ToBmpReader(in io.Reader, realDataSize int64) (io.Reader, int64, int64) {
	width, height := getBmpSize(realDataSize)
	bmpHead := buildBmpHead(width, height)
	dataSizeByteArray := intToByteArray(realDataSize)
	zeroNum := width*height*3 - realDataSize - 4
	needAddZero := make([]byte, zeroNum)

	// 构建前文件字节数组和输入流
	beforeFileByteArray := append(bmpHead, dataSizeByteArray...)
	beforeSize := len(beforeFileByteArray)
	beforeFileReader := bytes.NewReader(beforeFileByteArray) // 创建一个读取beforeFileByteArray内容的*Reader

	afterFileByteArray := needAddZero
	afterSize := len(afterFileByteArray)
	afterFileReader := bytes.NewReader(afterFileByteArray) // 创建一个读取afterFileByteArray内容的*Reader

	return io.MultiReader(beforeFileReader, io.LimitReader(in, realDataSize), afterFileReader), int64(beforeSize), int64(afterSize)
}
