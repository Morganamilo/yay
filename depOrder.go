package main

import (
	//	"fmt"
	"strconv"
	//	"strings"
	//	"sync"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
	//gopkg "github.com/mikkeloscar/gopkgbuild"
)

type depOrder struct {
	Aur      []*rpc.Pkg
	Repo     []*alpm.Package
	Missing  []string
	Runtime stringSet
}

func (do *depOrder) String() string {
	str := ""
	str += "\n" + red("Repo") + " (" + strconv.Itoa(len(do.Repo)) + ") :"
	for _,pkg := range do.Repo {
		str += " " + pkg.Name()
	}

	str += "\n" + red("Aur") + " (" + strconv.Itoa(len(do.Aur)) + ") :"
	for _,pkg := range do.Aur {
		str += " " + pkg.Name
	}

	str += "\n" + red("Runtime") + " (" + strconv.Itoa(len(do.Runtime)) + ") :"
	for pkg := range do.Runtime {
		str += " " + pkg
	}

	str += "\n" + red("Make") + " (" + strconv.Itoa(len(do.Repo) + len(do.Aur) - len(do.Runtime)) + ") :"
	for _,pkg := range do.Repo {
		if !do.Runtime.get(pkg.Name()) {
		str += " " + pkg.Name()
		}
	}
	for _,pkg := range do.Aur {
		if !do.Runtime.get(pkg.Name) {
		str += " " + pkg.Name
		}
	}




	return str
}

func makeDepOrder() *depOrder {
	return &depOrder{
		make([]*rpc.Pkg, 0),
		make([]*alpm.Package, 0),
		make([]string, 0),
		make(stringSet),
	}
}

func getDepOrder(dp *depPool) (*depOrder, error) {
	do := makeDepOrder()

	for _, target := range dp.Targets {
			dep := target.DepString()
			aurPkg := dp.findSatisfierAur(dep)
			if aurPkg != nil {
				do.orderPkgAur(aurPkg, dp, true)
			}
			
			repoPkg := dp.findSatisfierRepo(dep)
			if repoPkg != nil {
				do.orderPkgRepo(repoPkg, dp, true)
			}
	}

	return do, nil
}

func (do *depOrder) orderPkgAur(pkg *rpc.Pkg, dp *depPool, runtime bool) {
	if runtime {
		do.Runtime.set(pkg.Name)
	}
	do.Aur = append(do.Aur, pkg)
	delete(dp.Aur, pkg.Name)


	for _, deps := range [3][]string{pkg.Depends, pkg.MakeDepends, pkg.CheckDepends} {
		for _, dep := range deps {
			aurPkg := dp.findSatisfierAur(dep)
			if aurPkg != nil {
				do.orderPkgAur(aurPkg, dp, runtime)
			}
			
			repoPkg := dp.findSatisfierRepo(dep)
			if repoPkg != nil {
				do.orderPkgRepo(repoPkg, dp, runtime)
			}

			runtime = false
		}
	}

}

func (do *depOrder) orderPkgRepo(pkg *alpm.Package, dp *depPool, runtime bool) {
	if runtime {
		do.Runtime.set(pkg.Name())
	}
	do.Repo = append(do.Repo, pkg)
	delete(dp.Repo, pkg.Name())

	pkg.Depends().ForEach(func(dep alpm.Depend) (err error) {
		repoPkg := dp.findSatisfierRepo(dep.String())
		if repoPkg != nil {
			do.orderPkgRepo(repoPkg, dp, runtime)
		}
		
		return nil
	})
}
