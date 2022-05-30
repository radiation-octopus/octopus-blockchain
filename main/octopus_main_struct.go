package main

import (
	"github.com/radiation-octopus/octopus/log"
)

type MainService struct {
	MainDao *MainDao `autoInjectLang:"main.MainDao"`
	Test    string   `autoInjectCfg:"octopus.director.name"`
}

func (my *MainService) AddMain() {
	log.Info("MainService.AddMain()")
	my.MainDao.AddMain()
}

type MainDao struct {
	MainService *MainService `autoInjectLang:"main.MainService"`
}

func (my *MainDao) AddMain() {
	log.Info("MainDao.AddMain()")
}
