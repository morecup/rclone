package api

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func BodyList(slice []string) string {
	if len(slice) > 0 {
		nospace := strings.Join(slice, "\",\"")
		quoted := fmt.Sprintf("[\"%s\"]", nospace)
		return quoted
	} else {
		return "[]"
	}
}
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func BoolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// EncryptMd5 百度网盘的MD5加密，会对标准的md5做一些特殊的处理
func EncryptMd5(t string) string {
	// 检查长度是否为32
	if len(t) != 32 {
		return t
	}

	// 检查所有字符是否在0-9或a-f之间
	for _, ch := range t {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return t
		}
	}

	// 重新排列字符串的片段
	r := t[8:16] + t[0:8] + t[24:32] + t[16:24]
	n := strings.Builder{}

	// 对每个字符进行转换
	for e := 0; e < len(r); e++ {
		// 将十六进制字符转换为数字
		v, _ := strconv.ParseInt(string(r[e]), 16, 64)
		// 执行异或操作
		zj := int(v) ^ (15 & e)
		// 转换回十六进制字符串（小写）
		n.WriteString(strconv.FormatInt(int64(zj), 16))
	}

	result := n.String()
	// 转换第9位字符（索引9）
	if num, err := strconv.ParseInt(string(result[9]), 16, 64); err == nil {
		replacement := string(rune(103 + num)) // 103 = 'g'的ASCII码
		// 替换字符串的第9个字符
		return result[:9] + replacement + result[10:]
	}

	return result
}
func DecryptMd5(encrypted string) string {
	// 1. 检查输入是否为有效的加密格式
	if len(encrypted) != 32 {
		return encrypted
	}

	// 检查特殊字符位置(索引9)是否在g-v范围内
	if encrypted[9] < 'g' || encrypted[9] > 'v' {
		return encrypted
	}

	// 检查其他位置是否都是十六进制字符
	for i, ch := range encrypted {
		if i == 9 {
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return encrypted
		}
	}

	// 2. 恢复原始n字符串
	// 取出索引9的特殊字符，计算原始十六进制值
	originalDigit := int(encrypted[9] - 'g') // 减去'g'的偏移量
	n := encrypted[:9] + fmt.Sprintf("%x", originalDigit) + encrypted[10:]

	// 3. 重建r字符串
	r := strings.Builder{}
	for e := 0; e < len(n); e++ {
		// 将十六进制字符转换为数字
		v, _ := strconv.ParseInt(string(n[e]), 16, 64)
		// 反转异或操作
		original := int(v) ^ (15 & e)
		r.WriteString(fmt.Sprintf("%x", original))
	}
	rStr := r.String()

	// 4. 重新排列字符串片段恢复原始数据
	if len(rStr) != 32 {
		return encrypted
	}

	// 反转排列操作
	return rStr[8:16] + // 对应原始t[0:8]
		rStr[0:8] + // 对应原始t[8:16]
		rStr[24:32] + // 对应原始t[16:24]
		rStr[16:24] // 对应原始t[24:32]
}
func Md5(in io.ReadCloser, limitSize int) (string, error) {
	// 确保在函数结束时关闭 io.ReadCloser
	defer in.Close()

	// 创建一个新的 MD5 哈希对象
	hash := md5.New()

	buffer := make([]byte, limitSize)

	bytesRead, err := io.ReadFull(in, buffer)
	if err != nil {
		return "", err
	}
	hash.Write(buffer[:min(bytesRead, limitSize)])

	return hex.EncodeToString(hash.Sum(nil)), nil
}
