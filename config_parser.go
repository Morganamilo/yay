package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Morganamilo/go-pacmanconf/ini"
)

const systemConfigFile = "/etc/yay.conf"

type unknownOption struct {
	key string
}

func (io unknownOption) Error() string {
	return fmt.Sprintf("unknown option '%s'", io.key)
}

type invalidOption struct {
	key   string
	value string
}

func (io invalidOption) Error() string {
	if io.value == "" {
		return fmt.Sprintf("option '%s' requires a value", io.key)
	}
	return fmt.Sprintf("invalid value for '%s' : %s", io.key, io.value)
}

type yayConfig struct {
	AURURL   string
	BuildDir string

	Redownload string
	Rebuild    string
	RemoveMake string
	SortBy     string
	SortMode   string

	RequestSplitN      int
	CompletionInterval int

	SudoLoop        bool
	TimeUpdate      bool
	Devel           bool
	CleanAfter      bool
	GitClone        bool
	Provides        bool
	PGPFetch        bool
	CombinedUpgrade bool
	UseAsk          bool

	Editor     string
	MakepkgBin string
	PacmanBin  string
	TarBin     string
	GitBin     string
	GpgBin     string

	EditorFlags []string
	MFlags      []string
	GitFlags    []string
	GpgFlags    []string

	UpgradeMenu bool
	CleanMenu   bool
	DiffMenu    bool
	EditMenu    bool

	AnswerClean   string
	AnswerDiff    string
	AnswerEdit    string
	AnswerUpgrade string

	MakepkgConf string
	PacmanConf  string
}

func (conf *yayConfig) initConfig() error {
	return ini.ParseFile(systemConfigFile, parseCallback, conf)
}

func parseCallback(fileName string, line int, section string,
	key string, value string, data interface{}) (err error) {
	if line < 0 {
		return fmt.Errorf("unable to read file: %s: %s", fileName, section)
	}
	if key == "" && value == "" {
		return nil
	}

	if key == "Include" {
		return ini.ParseFile(value, parseCallback, data)
	}

	value = os.ExpandEnv(value)
	conf := data.(*yayConfig)

	if section == "options" {
		err = setOption(key, value, conf)
	} else if section == "menus" {
		err = setMenu(key, value, conf)
	} else if section == "bin" {
		err = setBin(key, value, conf)
	} else {
		err = fmt.Errorf("invalid section '%s'", section)
	}

	if err != nil {
		//return fmt.Errorf("%s:%d: %s", fileName, line, err.Error())
		fmt.Printf("%s:%d: %s\n", fileName, line, err.Error())
		return nil
	}

	return
}

func setOption(key string, value string, conf *yayConfig) error {
	var err error

	found := true
	switch key {
	case "AURURL":
		conf.AURURL = value
	case "BuildDir":
		conf.BuildDir = value
	case "SortMode":
		switch value {
		case "BottomUp", "TopDown":
			conf.SortMode = strings.ToLower(value)
		default:
			return invalidOption{key, value}
		}
	case "SortBy":
		switch value {
		case "Votes", "Popularity", "Name", "Base", "Submitted", "Modified", "Id", "BaseID":
			conf.SortBy = strings.ToLower(value)
		default:
			return invalidOption{key, value}
		}
	case "RequestSplitN":
		if conf.RequestSplitN, err = strconv.Atoi(value); err != nil || conf.RequestSplitN < 0 {
			return invalidOption{key, value}
		}
	case "CompletionInterval":
		if conf.RequestSplitN, err = strconv.Atoi(value); err != nil || conf.RequestSplitN < 0 {
			return invalidOption{key, value}
		}
	default:
		found = false
	}

	if found {
		if value == "" {
			return invalidOption{key, value}
		}
		return nil
	}

	found = true
	switch key {
	case "SudoLoop":
		conf.SudoLoop = true
	case "TimeUpdate":
		conf.TimeUpdate = true
	case "Devel":
		conf.Devel = true
	case "CleanAfter":
		conf.CleanAfter = true
	case "GitClone":
		conf.GitClone = true
	case "Provides":
		conf.Provides = true
	case "PGPFetch":
		conf.PGPFetch = true
	case "CombinedUpgrade":
		conf.CombinedUpgrade = true
	case "UseAsk":
		conf.CombinedUpgrade = true
	default:
		found = false
	}

	if found {
		if value != "" {
			return invalidOption{key, value}
		}
		return nil
	}

	switch key {
	case "Redownload":
		switch value {
		case "Yes", "No", "All":
			conf.Redownload = strings.ToLower(value)
		case "":
			conf.Redownload = "yes"
		default:
			return invalidOption{key, value}
		}
	case "Rebuild":
		switch value {
		case "Yes", "No", "All", "Tree":
			conf.Rebuild = strings.ToLower(value)
		case "":
			conf.Rebuild = "yes"
		default:
			return invalidOption{key, value}
		}
	case "RemoveMake":
		switch value {
		case "Yes", "No", "Ask":
			conf.RemoveMake = strings.ToLower(value)
		case "":
			conf.RemoveMake = "yes"
		default:
			return invalidOption{key, value}
		}
	default:
		return unknownOption{key}
	}

	return nil
}

func setMenu(key string, value string, conf *yayConfig) error {
	switch key {
	case "Upgrade":
		conf.UpgradeMenu = true
		conf.AnswerUpgrade = value
	case "Clean":
		conf.CleanMenu = true
		conf.AnswerClean = value
	case "Diff":
		conf.DiffMenu = true
		conf.AnswerDiff = value
	case "Edit":
		conf.EditMenu = true
		conf.AnswerEdit = value
	default:
		return unknownOption{key}
	}
	return nil
}

func appendFields(in []string, str string) []string {
	return append(in, strings.Fields(str)...)
}

func setBin(key string, value string, conf *yayConfig) error {
	switch key {
	case "Editor":
		conf.Editor = value
	case "Makepkg":
		conf.MakepkgBin = value
	case "Pacman":
		conf.PacmanBin = value
	case "Tar":
		conf.TarBin = value
	case "Git":
		conf.GitBin = value
	case "Gpg":
		conf.GpgBin = value
	case "EditorFlags":
		conf.EditorFlags = appendFields(conf.EditorFlags, value)
	case "MFlags":
		conf.MFlags = appendFields(conf.MFlags, value)
	case "GitFlags":
		conf.GitFlags = appendFields(conf.GitFlags, value)
	case "GpgFlags":
		conf.GpgFlags = appendFields(conf.GpgFlags, value)
	case "MakepkgConf":
		conf.MakepkgConf = value
	case "PacmanConf":
		conf.PacmanConf = value
	default:
		return unknownOption{key}
	}

	if value == "" {
		return invalidOption{key, value}
	}

	return nil
}
