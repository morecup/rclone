package api

import "testing"

func TestEncryptMd5(t *testing.T) {
	type args struct {
		e string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "标准32位十六进制字符串",
			args: args{e: "0535ba4186c95852f6778ed91a2d19c4"},
			want: "87ea1d358s9e77ae1b0e5ca37fdc4336", // 这是示例值，需要根据实际算法计算正确值
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncryptMd5(tt.args.e); got != tt.want {
				t.Errorf("EncryptMd5() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecryptMd5(t *testing.T) {
	type args struct {
		encrypted string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "标准32位十六进制字符串",
			args: args{encrypted: "87ea1d358s9e77ae1b0e5ca37fdc4336"},
			want: "0535ba4186c95852f6778ed91a2d19c4", // 这是示例值，需要根据实际算法计算正确值
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecryptMd5(tt.args.encrypted); got != tt.want {
				t.Errorf("decryptMd5() = %v, want %v", got, tt.want)
			}
		})
	}
}
