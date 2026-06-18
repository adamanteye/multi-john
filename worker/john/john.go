package john

import (
	"bufio"
	"io"
	"os"
	"os/exec"

	"go.uber.org/zap"
)

var (
	potFile = "some" + ".pot"
	potFlag = "--pot=" + potFile
)

type Cmd struct {
	Bin      string
	File     string
	Args     []string
	Env      []string
	KillChan chan bool
	Log      *zap.SugaredLogger
	Results  chan []string
}

func New(bin string, file string, args []string, logger *zap.Logger) Cmd {
	return Cmd{
		Bin:     bin,
		File:    file,
		Args:    args,
		Results: make(chan []string),
		Log:     logger.Sugar(),
	}
}

func (c *Cmd) args() []string {
	res := []string{c.File, potFlag}
	res = append(res, c.Args...)
	c.Log.Debug(res)
	return res
}

func (c *Cmd) showArgs() []string {
	return []string{c.File, "--show", potFlag}
}

func (c *Cmd) Run() error {
	f, err := os.Create(potFile)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	c.WatchPotfile()
	cmd := exec.Command(c.Bin, c.args()...)
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	watch := func(stdx io.ReadCloser) {
		scanner := bufio.NewScanner(stdx)
		for scanner.Scan() {
			m := scanner.Text()
			c.Log.Info(m)
		}
	}
	go watch(stderr)
	go watch(stdout)
	c.Log.Debug("starting john")
	if err := cmd.Run(); err != nil {
		return err
	}
	c.Results <- c.ReadPotfile()
	c.Log.Debug("finished running john")
	return nil
}
