package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"

	"github.com/billziss-gh/cgofuse/fuse"
	fileAPI "github.com/meateam/api-gateway/file"
	uploadAPI "github.com/meateam/api-gateway/upload"
)

const (
	folderContentType = "application/vnd.drive.folder"
	driveAPI          = ""
	token             = ``
	MB                = 1 << 20
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
	pathFileMap   map[string]*File
	pathUploadMap map[string]chan []byte
}

// uploadInitBody is a structure of the json body of upload init request.
type uploadInitBody struct {
	Title    string `json:"title"`
	MimeType string `json:"mimeType"`
}

func resize(slice []byte, size int64, zeroinit bool) []byte {
	const allocunit = 64 * 1024
	allocsize := (size + allocunit - 1) / allocunit * allocunit
	if cap(slice) != int(allocsize) {
		var newslice []byte
		{
			defer func() {
				if r := recover(); nil != r {
					panic(fuse.Error(-fuse.ENOSPC))
				}
			}()
			newslice = make([]byte, size, allocsize)
		}
		copy(newslice, slice)
		slice = newslice
	} else if zeroinit {
		i := len(slice)
		slice = slice[:size]
		for ; len(slice) > i; i++ {
			slice[i] = 0
		}
	}
	return slice
}

func updateFile(fileStats *File, file *fileAPI.GetFileByIDResponse) {
	if file == nil || fileStats == nil {
		return
	}

	fileStats.File = *file
	fileStats.stat.Birthtim = fuse.Timespec{Sec: file.CreatedAt / 1000, Nsec: file.CreatedAt % 1000}
	fileStats.stat.Ctim = fuse.Timespec{Sec: file.UpdatedAt / 1000, Nsec: file.UpdatedAt % 1000}
	fileStats.stat.Mtim = fuse.Timespec{Sec: file.UpdatedAt / 1000, Nsec: file.UpdatedAt % 1000}
	fileStats.stat.Size = file.Size
}

func (fs *DriveFS) uploadMultipart(path string, buff []byte) {
	body := &bytes.Buffer{}

	writer := multipart.NewWriter(body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
			escapeQuotes("file"), escapeQuotes(filepath.Base(path))))
	h.Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
	part, err := writer.CreatePart(h)
	if err != nil {
		return
	}

	_, err = part.Write(buff)
	if err != nil {
		return
	}

	if err := writer.Close(); err != nil {
		return
	}

	req, err := http.NewRequest("POST", driveAPI+"/api/upload?uploadType=multipart", body)
	if err != nil {
		return
	}

	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.Header.Add("Authorization", token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()

	fileIDBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return
	}

	fileID := string(fileIDBytes)

	req, err = http.NewRequest("GET", driveAPI+"/api/files/"+fileID, nil)
	if err == nil {
		req.Header.Add("Authorization", token)
	}

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer res.Body.Close()

	var file fileAPI.GetFileByIDResponse
	respBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return
	}

	if err = json.Unmarshal(respBytes, &file); err != nil {
		log.Println(err)
		return
	}

	updateFile(fs.pathFileMap[path], &file)

	return
}

func (fs *DriveFS) uploadResumable(path string, c <-chan []byte) {
	size := fs.pathFileMap[path].stat.Size
	uploadInitRequest := uploadInitBody{
		MimeType: mime.TypeByExtension(filepath.Ext(path)),
		Title:    filepath.Base(path),
	}

	uploadInitRequestBody, err := json.Marshal(uploadInitRequest)
	if err != nil {
		log.Println(err)
		return
	}

	req, err := http.NewRequest("POST", driveAPI+"/api/upload", bytes.NewBuffer(uploadInitRequestBody))
	if err != nil {
		log.Println(err)
		return
	}
	req.Header.Add("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(uploadAPI.ContentLengthCustomHeader, strconv.FormatInt(size, 10))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer res.Body.Close()

	uploadID := res.Header.Get(uploadAPI.UploadIDCustomHeader)

	pr, pw := io.Pipe()
	multipartWriter := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()

		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
				escapeQuotes("file"), escapeQuotes(filepath.Base(path))))
		h.Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
		part, err := multipartWriter.CreatePart(h)
		if err != nil {
			pw.CloseWithError(err)
			log.Println(err)
			return
		}

		for buff := range c {
			if _, err = part.Write(buff); err != nil {
				pw.CloseWithError(err)
				log.Println(err)
				return
			}
		}

		if err := multipartWriter.Close(); err != nil {
			pw.CloseWithError(err)
			log.Println(err)
			return
		}
	}()

	req, err = http.NewRequest("POST", driveAPI+"/api/upload?uploadType=resumable&uploadId="+uploadID, pr)
	if err != nil {
		log.Println(err)
		return
	}

	req.Header.Add("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Add(uploadAPI.ContentRangeHeader, fmt.Sprintf("bytes 0-%d/%d", size-1, size))
	req.Header.Add("Authorization", token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		pr.CloseWithError(err)
		log.Println(err)
		return
	}
	pr.Close()
	defer res.Body.Close()

	fileIDBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return
	}

	fileID := string(fileIDBytes)

	req, err = http.NewRequest("GET", driveAPI+"/api/files/"+fileID, nil)
	if err == nil {
		req.Header.Add("Authorization", token)
	}

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer res.Body.Close()

	var file fileAPI.GetFileByIDResponse
	respBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return
	}

	if err = json.Unmarshal(respBytes, &file); err != nil {
		log.Println(err)
		return
	}

	updateFile(fs.pathFileMap[path], &file)

	return
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

	updateFile(fileStats, file)

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
		log.Println(err)
		return
	}

	var files []fileAPI.GetFileByIDResponse
	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
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
	f, ok := fs.pathFileMap[path]
	if !ok {
		return 0
	}

	fs.pathFileMap[path].data = resize(fs.pathFileMap[path].data, size, true)
	f.stat.Size = size

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
		log.Println(err)
		return
	}

	file.data, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
		return
	}

	n = copy(buff, file.data[ofst:endofst])
	return
}

func (fs *DriveFS) Write(path string, buff []byte, ofst int64, fh uint64) (n int) {
	size := fs.pathFileMap[path].stat.Size
	if size <= uploadAPI.MaxSimpleUploadSize {
		fs.uploadMultipart(path, buff)
	} else {
		if ofst == 0 {
			fs.pathUploadMap[path] = make(chan []byte, size/MB)
			go fs.uploadResumable(path, fs.pathUploadMap[path])
		}

		b := make([]byte, len(buff), len(buff))
		copy(b, buff)
		fs.pathUploadMap[path] <- b
	}

	endofst := ofst + int64(len(buff))
	// Doesn't work with edit
	if endofst >= size {
		fs.pathFileMap[path].data = resize(fs.pathFileMap[path].data, endofst, true)
		fs.pathFileMap[path].stat.Size = endofst
		close(fs.pathUploadMap[path])
	}

	n = copy(fs.pathFileMap[path].data[ofst:endofst], buff)

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
		pathFileMap:   make(map[string]*File),
		pathUploadMap: make(map[string]chan []byte),
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
