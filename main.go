package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/billziss-gh/cgofuse/fuse"
	fileAPI "github.com/meateam/api-gateway/file"
)

const (
	folderContentType = "application/vnd.drive.folder"
	driveAPI          = ""
	token             = ``
)

type File struct {
	File       fileAPI.GetFileByIDResponse
	stat       fuse.Stat_t
	data       []byte
	parentFile *File
	Children   map[string]*File
}

// DriveFS is a struct that holds and handles the Drive FileSystem Mount,
// It implements fuse.FileSystemInterface.
type DriveFS struct {
	fuse.FileSystemBase
	pathFileMap map[string]*File
}

func (fs *DriveFS) newNode(path string, mode uint32, dev uint64, file *fileAPI.GetFileByIDResponse) {
	tmsp := fuse.Now()
	uid, gid, _ := fuse.Getcontext()
	dir := filepath.ToSlash(filepath.Dir(path))
	parent := fs.pathFileMap[dir]
	name := filepath.Base(path)

	fileStats := &File{
		File: fileAPI.GetFileByIDResponse{
			Name: name,
		},
		Children:   make(map[string]*File),
		parentFile: parent,
		stat: fuse.Stat_t{
			Dev:      dev,
			Ino:      uint64(len(fs.pathFileMap) + 1),
			Mode:     mode,
			Nlink:    1,
			Uid:      uid,
			Gid:      gid,
			Atim:     tmsp,
			Mtim:     tmsp,
			Ctim:     tmsp,
			Birthtim: tmsp,
			Flags:    0,
			Size:     0,
		},
	}

	if file != nil {
		fileStats.File = *file
		fileStats.stat.Birthtim = fuse.Timespec{Sec: file.CreatedAt / 1000, Nsec: file.CreatedAt % 1000}
		fileStats.stat.Ctim = fuse.Timespec{Sec: file.UpdatedAt / 1000, Nsec: file.UpdatedAt % 1000}
		fileStats.stat.Mtim = fuse.Timespec{Sec: file.UpdatedAt / 1000, Nsec: file.UpdatedAt % 1000}
		fileStats.stat.Size = file.Size
	}

	if parent != nil {
		parent.Children[name] = fileStats
	}

	fs.pathFileMap[path] = fileStats

	return
}

func (fs *DriveFS) Init() {
	fs.newNode("/", fuse.S_IFDIR|0777, 0, nil)
	req, err := http.NewRequest("GET", driveAPI+"/api/files", nil)
	if err == nil {
		req.Header.Add("Authorization", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}

	var files []fileAPI.GetFileByIDResponse
	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if err = json.Unmarshal(respBytes, &files); err != nil {
		return
	}

	for i := 0; i < len(files); i++ {
		mode := uint32(fuse.S_IFREG | 0777)
		if files[i].Type == folderContentType {
			mode = fuse.S_IFDIR | 0777
		}

		fs.newNode("/"+files[i].Name, mode, 0, &files[i])
	}
}

func (fs *DriveFS) Destroy() {

}

func (fs *DriveFS) Statfs(path string, stat *fuse.Statfs_t) int {
	stat.Bsize = 1
	stat.Frsize = 1
	stat.Blocks = 500000000000
	stat.Bfree = 219430400000

	return 0
}

func (fs *DriveFS) Mknod(path string, mode uint32, dev uint64) int {
	fs.newNode(path, mode, dev, nil)

	return 0
}

func (fs *DriveFS) Mkdir(path string, mode uint32) int {
	fs.newNode(path, fuse.S_IFDIR|(mode&0777), 0, nil)

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
	if f, ok := fs.pathFileMap[path]; ok {
		return 0, f.stat.Ino
	}

	return -fuse.ENOENT, ^uint64(0)
}

func (fs *DriveFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) (errc int) {
	if len(fs.pathFileMap[path].Children) == 0 && fs.pathFileMap[path].File.Type == folderContentType {
		req, err := http.NewRequest("GET", driveAPI+"/api/files?parent="+fs.pathFileMap[path].File.ID, nil)
		if err == nil {
			req.Header.Add("Authorization", token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}

		var files []fileAPI.GetFileByIDResponse
		respBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return
		}

		if err = json.Unmarshal(respBytes, &files); err != nil {
			return
		}

		fs.pathFileMap[path].Children = make(map[string]*File)

		for i := 0; i < len(files); i++ {
			mode := uint32(fuse.S_IFREG | 0777)
			if files[i].Type == folderContentType {
				mode = fuse.S_IFDIR | 0777
			}

			fs.newNode(path+"/"+files[i].Name, mode, 0, &files[i])
		}
	}

	*stat = fs.pathFileMap[path].stat

	return 0
}

func (fs *DriveFS) Truncate(path string, size int64, fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Read(path string, buff []byte, ofst int64, fh uint64) (n int) {
	file := fs.pathFileMap[path]

	endofst := ofst + int64(len(buff))
	if endofst > file.stat.Size {
		endofst = file.stat.Size
	}

	if endofst < ofst {
		return 0
	}

	if file.data != nil {
		n = copy(buff, file.data[ofst:endofst])
		return
	}

	req, err := http.NewRequest("GET", driveAPI+"/api/files/"+fs.pathFileMap[path].File.ID+"?alt=media", nil)
	if err == nil {
		req.Header.Add("Authorization", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}

	file.data, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	n = copy(buff, file.data[ofst:endofst])
	return
}

func (fs *DriveFS) Write(path string, buff []byte, ofst int64, fh uint64) (n int) {
	return
}

func (fs *DriveFS) Release(path string, fh uint64) (errc int) {
	return 0
}

func (fs *DriveFS) Opendir(path string) (errc int, fh uint64) {
	return 0, fs.pathFileMap[path].stat.Ino
}

func (fs *DriveFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64) (errc int) {
	node := fs.pathFileMap[path]
	fill(".", &node.stat, 0)
	fill("..", nil, 0)
	for _, chld := range node.Children {
		if !fill(chld.File.Name, &chld.stat, 0) {
			break
		}
	}
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
	host.Mount("K:", os.Args[1:])
}
