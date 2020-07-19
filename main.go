package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/billziss-gh/cgofuse/fuse"
)

const (
	folderContentType = "application/vnd.drive.folder"
	driveAPI = ""
	token    = ``
)

type File struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      uint64 `json:"size"`
	OwnerID   string `json:"ownerId"`
	Parent    string `json:"parent"`
	Role      string `json:"role"`
	CreatedAt uint64 `json:"createdAt"`
	UpdatedAt uint64 `json:"updatedAt"`
	stat fuse.Stat_t
	data []byte
	Children  map[string]*File
}

// DriveFS is a struct that holds and handles the Drive FileSystem Mount,
// It implements fuse.FileSystemInterface.
type DriveFS struct {
	fuse.FileSystemBase
	pathFileMap map[string]*File
}

func (fs *DriveFS) Init() {
	tmsp := fuse.Now()
	fs.pathFileMap["/"] = &File{
		stat: fuse.Stat_t{
			Dev:      0,
			Ino:      1,
			Mode:     fuse.S_IFDIR|00777,
			Nlink:    1,
			Uid:      0,
			Gid:      0,
			Atim:     tmsp,
			Mtim:     tmsp,
			Ctim:     tmsp,
			Birthtim: tmsp,
			Flags:    0,
		},
	}
	req, err := http.NewRequest("GET", "http://noga.northeurope.cloudapp.azure.com/api/files", nil)
	if err == nil {
		req.Header.Add("Authorization", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}

	var files []File
	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if err = json.Unmarshal(respBytes, &files); err != nil {
		return
	}

	for i := 0; i < len(files); i++ {
		if files[i].Type == folderContentType {
			code := fs.Mkdir("\\" + files[i].Name, fuse.S_IFDIR|07777)
			if code != 0 {
				return
			}

		} else {
			code := fs.Mknod("\\" + files[i].Name, fuse.S_IFREG, 0)
			if code != 0 {
				return
			}
		}

		fs.pathFileMap["/" + files[i].Name] = &files[i]
	}
}

func (fs *DriveFS) Destroy() {

}

func (fs *DriveFS) Statsfs(path string, stat *fuse.Statfs_t) int {
	stat.Bsize = 4096
	stat.Frsize = 4096
	stat.Blocks = 2097152
	stat.Bfree = 2097152

	return 0
}

func (fs *DriveFS) Mknod(path string, mode uint32, dev uint64) int {
	
	return 0
}

func (fs *DriveFS) Mkdir(path string, mode uint32) int {
	
	
	return 0
}

func (fs *DriveFS) Unlink(path string) (errc int) {
	return 0
}

func (fs *DriveFS) Rmdir(path string) (errc int) {
	return 0
}

func (fs *DriveFS) Link(oldpath string, newpath string) (errc int) {
	return 0
}

func (fs *DriveFS) Symlink(target string, newpath string) (errc int) {
	return 0
}

func (fs *DriveFS) Readlink(path string) (errc int, target string) {
	return 0, ""
}

func (fs *DriveFS) Rename(oldpath string, newpath string) (errc int) {
	return 0
}

func (fs *DriveFS) Chmod(path string, mode uint32) (errc int) {
	return 0
}

func (fs *DriveFS) Chown(path string, uid uint32, gid uint32) (errc int) {
	return 0
}

func (fs *DriveFS) Utimens(path string, tmsp []fuse.Timespec) (errc int) {
	return 0
}

func (fs *DriveFS) Open(path string, flags int) (errc int, fh uint64) {
	return 0, uint64(0)
}

func (fs *DriveFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) (errc int) {
	*stat = fs.pathFileMap[path].stat
	return 0
}

func (fs *DriveFS) Truncate(path string, size int64, fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Read(path string, buff []byte, ofst int64, fh uint64) (n int) {
	return
}

func (fs *DriveFS) Write(path string, buff []byte, ofst int64, fh uint64) (n int) {
	return
}

func (fs *DriveFS) Release(path string, fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Opendir(path string) (errc int, fh uint64) {
	return 0, uint64(0)
}

func (fs *DriveFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Releasedir(path string, fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Setxattr(path string, name string, value []byte, flags int) (errc int) {
	return 0
}

func (fs *DriveFS) Getxattr(path string, name string) (errc int, xatr []byte) {
	return 0, xatr
}

func (fs *DriveFS) Removexattr(path string, name string) (errc int) {
	return 0
}

func (fs *DriveFS) Listxattr(path string, fill func(name string) bool) (errc int) {
	return 0
}

func (fs *DriveFS) Chflags(path string, flags uint32) (errc int) {
	return 0
}

func (fs *DriveFS) Setcrtime(path string, tmsp fuse.Timespec) (errc int) {
	return 0
}

func (fs *DriveFS) Setchgtime(path string, tmsp fuse.Timespec) (errc int) {
	return 0
}

func NewDriveFS() (*DriveFS, error) {
	fs := &DriveFS{
		pathFileMap: make(map[string]*File),
	}

	return fs, nil
}

func main() {
	fs, err := NewDriveFS()
	if err != nil {
		panic(err)
	}

	host := fuse.NewFileSystemHost(fs)
	host.SetCapReaddirPlus(true)
	host.Mount("", os.Args[1:])
}
