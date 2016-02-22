package scanner

import (
	"encoding/gob"
	"errors"
	"fmt"
	"github.com/Symantec/Dominator/lib/image"
	"github.com/Symantec/Dominator/objectserver"
	"log"
	"os"
	"path"
	"runtime"
	"syscall"
	"time"
)

type concurrencyState struct {
	semaphore    chan struct{}
	errorChannel chan error
	pending      uint64
}

func newConcurrencyState() *concurrencyState {
	state := new(concurrencyState)
	state.semaphore = make(chan struct{}, runtime.NumCPU())
	state.errorChannel = make(chan error)
	return state
}

func (state *concurrencyState) goFunc(doFunc func(string) error,
	filename string) error {
	for {
		select {
		case err := <-state.errorChannel:
			state.pending--
			if err != nil {
				return err
			}
		case state.semaphore <- struct{}{}:
			state.pending++
			go func(filename string) {
				state.errorChannel <- doFunc(filename)
				<-state.semaphore
			}(filename)
			return nil
		}
	}
}

func (state *concurrencyState) close() error {
	close(state.semaphore)
	for ; state.pending > 0; state.pending-- {
		if err := <-state.errorChannel; err != nil {
			return err
		}
	}
	close(state.errorChannel)
	return nil
}

func loadImageDataBase(baseDir string, objSrv objectserver.ObjectServer,
	logger *log.Logger) (*ImageDataBase, error) {
	fi, err := os.Stat(baseDir)
	if err != nil {
		return nil, errors.New(
			fmt.Sprintf("Cannot stat: %s\t%s\n", baseDir, err))
	}
	if !fi.IsDir() {
		return nil, errors.New(fmt.Sprintf("%s is not a directory\n", baseDir))
	}
	imdb := new(ImageDataBase)
	imdb.baseDir = baseDir
	imdb.imageMap = make(map[string]*image.Image)
	imdb.addNotifiers = make(notifiers)
	imdb.deleteNotifiers = make(notifiers)
	imdb.objectServer = objSrv
	imdb.logger = logger
	state := newConcurrencyState()
	startTime := time.Now()
	var rusageStart, rusageStop syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &rusageStart)
	if err := imdb.scanDirectory("", state); err != nil {
		return nil, err
	}
	if err := state.close(); err != nil {
		return nil, err
	}
	if logger != nil {
		plural := ""
		if imdb.CountImages() != 1 {
			plural = "s"
		}
		syscall.Getrusage(syscall.RUSAGE_SELF, &rusageStop)
		userTime := time.Duration(rusageStop.Utime.Sec)*time.Second +
			time.Duration(rusageStop.Utime.Usec)*time.Microsecond -
			time.Duration(rusageStart.Utime.Sec)*time.Second -
			time.Duration(rusageStart.Utime.Usec)*time.Microsecond
		logger.Printf("Loaded %d image%s in %s (%s user CPUtime)\n",
			imdb.CountImages(), plural, time.Since(startTime), userTime)
	}
	return imdb, nil
}

func (imdb *ImageDataBase) scanDirectory(dirname string,
	state *concurrencyState) error {
	file, err := os.Open(path.Join(imdb.baseDir, dirname))
	if err != nil {
		return err
	}
	names, err := file.Readdirnames(-1)
	file.Close()
	for _, name := range names {
		filename := path.Join(dirname, name)
		var stat syscall.Stat_t
		err := syscall.Lstat(path.Join(imdb.baseDir, filename), &stat)
		if err != nil {
			if err == syscall.ENOENT {
				continue
			}
			return err
		}
		if stat.Mode&syscall.S_IFMT == syscall.S_IFDIR {
			err = imdb.scanDirectory(filename, state)
		} else if stat.Mode&syscall.S_IFMT == syscall.S_IFREG {
			err = state.goFunc(imdb.loadFile, filename)
		}
		if err != nil {
			if err == syscall.ENOENT {
				continue
			}
			return err
		}
	}
	return nil
}

func (imdb *ImageDataBase) loadFile(filename string) error {
	file, err := os.Open(path.Join(imdb.baseDir, filename))
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := gob.NewDecoder(file)
	var image image.Image
	if err = decoder.Decode(&image); err != nil {
		return err
	}
	image.FileSystem.RebuildInodePointers()
	imdb.Lock()
	defer imdb.Unlock()
	imdb.imageMap[filename] = &image
	return nil
}
