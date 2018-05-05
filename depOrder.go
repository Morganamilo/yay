package main

import (
	//	"fmt"
	//	"strconv"
	//	"strings"
	//	"sync"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
	//gopkg "github.com/mikkeloscar/gopkgbuild"
)

type depOrder struct {
	Aur      []*rpc.Pkg
	repo     []*alpm.Package
	Missing  []string
	MakeOnly stringSet
}

func makeDepOrder() *depOrder {
	return &depOrder{
		make([]*rpc.Pkg, 0),
		make([]*alpm.Package, 0),
		make([]string, 0),
		make(stringSet),
	}
}

func orderPkgs(dp *depPool) {
	do := makeDepOrder()

	for _, pkg := range dp.Repo {
		do.orderRepoPkg(pkg, dp)
	}

}

/*func (dp *depOrder) orderPkg(pkg string, dp *depPool) {
	aurPkg = dp.findSatisfierAur(pkg)
	if aurPkg != nil {
		delete(dp.Aur, aurPkg.Name)
		do.Aur = append(do.Aur, aurPkg)
	}

	repoPkg = dp.findSatisfierRepo(pkg)
	if repoPkg != nil {
		delete(dp.Repo, repoPkg)
		do.Repo = append(do.Repo, repoPkg)
	}

}

func (do *depOrder) orderAurPkg(pkg *rpc.Pkg, dp *depPool) {
	for _, deps := range [3][]string{pkg.Depends, pkg.MakeDepends, pkg.CheckDepends} {
		for _, pkg := range deps {
			orderAurPkg(pkg, dp)
		}
	}

	orderPkg()

}*/

func (do *depOrder) orderRepoPkg(pkg *alpm.Package, dp *depPool) {
	pkg.Depends().ForEach(func(dep alpm.Depend) (err error) {
		pkg := dp.findSatisfierRepo(dep.String())
		if pkg == nil {
			return nil
		}
		
		do.orderRepoPkg(pkg, dp)
		return nil
	})

	do.repo = append(do.repo, pkg)
	delete(dp.Repo, pkg.Name())

}
