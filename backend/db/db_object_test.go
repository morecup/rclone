package db

import (
	"errors"
	"github.com/rclone/rclone/lib/paths"
	"testing"
)

func TestResolveDerivedPathFromRelative(t *testing.T) {
	type args struct {
		aPath        string
		bPath        string
		cPath        string
		isDirResolve bool
	}
	tests := []struct {
		name      string
		args      args
		want      string
		wantErr   bool
		wantError error
	}{
		{
			name: "正常路径转化",
			args: args{
				aPath:        `\\?\D:\test\testlink\30MBLink.txt`,
				bPath:        `D:\test\30MB.txt`,
				cPath:        `/copylinkdir/30MBLink.txt`,
				isDirResolve: false,
			},
			want:    "/30MB.txt",
			wantErr: false,
		},
		{
			name: "切换盘符路径转化",
			args: args{
				aPath:        `\\?\D:\test\30MBLink.txt`,
				bPath:        `C:\test\30MB.txt`,
				cPath:        `/dir1/dir2/30MBLink.txt`,
				isDirResolve: false,
			},
			want:    "/C/test/30MB.txt",
			wantErr: false,
		},
		{
			name: "文件夹路径转化",
			args: args{
				aPath:        `\\?\D:\test`,
				bPath:        `C:\test`,
				cPath:        `/dir1/dir2/dir3`,
				isDirResolve: true,
			},
			want:    "/dir1/C/test",
			wantErr: false,
		},
		{
			name: "文件夹路径转化,aPath和cPath被去掉最后一块 可能的问题",
			args: args{
				aPath:        `\\?\D:\test\test1`,
				bPath:        `D:\test\test1\test3\test4`,
				cPath:        `/dir1/dir2/dir3`,
				isDirResolve: true,
			},
			want:    "/dir1/dir2/dir3/test3/test4",
			wantErr: false,
		},
		{
			name: "超出限定时，需要抛出异常",
			args: args{
				aPath:        `\\?\D:\test\test1`,
				bPath:        `D:\test2`,
				cPath:        `/dir1`,
				isDirResolve: true,
			},
			want:      "",
			wantErr:   true,
			wantError: paths.ExceedsRootDirError,
		},
		{
			name: "超出限定时，需要抛出异常",
			args: args{
				aPath:        `\\?\D:\test\test1`,
				bPath:        `D:\test2`,
				cPath:        `dir1`,
				isDirResolve: true,
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDerivedPathFromRelative(tt.args.aPath, tt.args.bPath, tt.args.cPath, tt.args.isDirResolve)
			if err != nil && true == tt.wantErr {
				if tt.wantError != nil {
					if errors.Is(err, tt.wantError) {

					} else {
						t.Errorf("ResolveDerivedPathFromRelative() error = %v, wantError %v", err, tt.wantError)
						return
					}
				}
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveDerivedPathFromRelative() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ResolveDerivedPathFromRelative() got = %v, want %v", got, tt.want)
			}
		})
	}
}
