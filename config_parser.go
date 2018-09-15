package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/Morganamilo/go-pacmanconf/ini"
)

func parseCallback(fileName string, line int, section string,
	key string, value string, data interface{}) (err error) {
	if line < 0 {
		return fmt.Errorf("unable to read file: %s: %s", fileName, section)
	}

	if key == "" && value == "" {
		return nil
	}

	if section == "options" {
		err = config.setOption(key, value)
	} else if section == "menus" {
		err = config.setMenus(key, value)
	} else if section == "answer" {
		err = config.setAnswer(key, value)
	} else {
		err = fmt.Errorf("line %d is not in a section: %s", line, fileName)
	}

	return
}

func (y *yayConfig) setMenus(key string, value string) error {
	switch key {
	case "Clean", "Diff", "Edit", "Upgrade":
		y.boolean[key+"Menu"] = true
		return nil
	}
	return fmt.Errorf("%d does not belong in the answer section", key)
}

func (y *yayConfig) setAnswer(key string, value string) error {
	switch key {
	case "Clean", "Diff", "Edit", "Upgrade":
		y.value[key] = value
		return nil
	}

	return fmt.Errorf("%d does not belong in the answer section", key)

}

func (y *yayConfig) setOption(key string, value string) error {
	switch key {
	case "BottomUp":
		y.num["SortMode"] = bottomUp
	case "TopDown":
		y.num["SortMode"] = topDown
	}

	y.value[key] = value
	return nil
}

func initConfigv2() error {
	iniBytes, err := ioutil.ReadFile(config.file)
	if !os.IsNotExist(err) || err != nil {
		return fmt.Errorf("Failed to open config file '%s': %s", config.file, err)
	}

	// Toggle all switches false
	for k := range config.boolean {
		config.boolean[k] = false
	}

	err = ini.Parse(string(iniBytes), parseCallback, nil)

	return err
}
