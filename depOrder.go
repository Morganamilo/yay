package main

import (
	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
)

type Base []*rpc.Pkg

func (b Base) Pkgbase() string {
	return b[0].PackageBase
}

func (b Base) Version() string {
	return b[0].Version
}

func (b Base) URLPath() string {
	return b[0].URLPath
}

type depOrder struct {
	Aur       []string
	Repo      []*alpm.Package
	Runtime   stringSet
	Bases     map[string][]*rpc.Pkg
	REALBASES []Base
}

func makeDepOrder() *depOrder {
	return &depOrder{
		make([]string, 0),
		make([]*alpm.Package, 0),
		make(stringSet),
		make(map[string][]*rpc.Pkg),
		make([]Base, 0),
	}
}

func getDepOrder(dp *depPool) *depOrder {
	do := makeDepOrder()

	for _, target := range dp.Targets {
		dep := target.DepString()
		aurPkg := dp.Aur[dep]
		if aurPkg != nil && pkgSatisfies(aurPkg.Name, aurPkg.Version, dep) {
			do.orderPkgAur(aurPkg, dp, true)
		}

		aurPkg = dp.findSatisfierAur(dep)
		if aurPkg != nil {
			do.orderPkgAur(aurPkg, dp, true)
		}

		repoPkg := dp.findSatisfierRepo(dep)
		if repoPkg != nil {
			do.orderPkgRepo(repoPkg, dp, true)
		}
	}

	for _, pkg := range do.Aur {
		do.REALBASES = append(do.REALBASES, do.Bases[pkg])
	}

	return do
}

func (do *depOrder) orderPkgAur(pkg *rpc.Pkg, dp *depPool, runtime bool) {
	if runtime {
		do.Runtime.set(pkg.Name)
	}
	delete(dp.Aur, pkg.Name)

	for i, deps := range [3][]string{pkg.Depends, pkg.MakeDepends, pkg.CheckDepends} {
		for _, dep := range deps {
			aurPkg := dp.findSatisfierAur(dep)
			if aurPkg != nil {
				do.orderPkgAur(aurPkg, dp, runtime && i == 0)
			}

			repoPkg := dp.findSatisfierRepo(dep)
			if repoPkg != nil {
				do.orderPkgRepo(repoPkg, dp, runtime && i == 0)
			}
		}
	}

	if _, ok := do.Bases[pkg.PackageBase]; !ok {
		do.Aur = append(do.Aur, pkg.PackageBase)
	}
	do.Bases[pkg.PackageBase] = append(do.Bases[pkg.PackageBase], pkg)
}

func (do *depOrder) orderPkgRepo(pkg *alpm.Package, dp *depPool, runtime bool) {
	if runtime {
		do.Runtime.set(pkg.Name())
	}
	delete(dp.Repo, pkg.Name())

	pkg.Depends().ForEach(func(dep alpm.Depend) (err error) {
		repoPkg := dp.findSatisfierRepo(dep.String())
		if repoPkg != nil {
			do.orderPkgRepo(repoPkg, dp, runtime)
		}

		return nil
	})

	do.Repo = append(do.Repo, pkg)
}

func (do *depOrder) HasMake() bool {
	lenAur := 0
	for _, base := range do.Bases {
		lenAur += len(base)
	}

	return len(do.Runtime) != lenAur+len(do.Repo)
}

func (do *depOrder) getMake() []string {
	makeOnly := make([]string, 0, len(do.Bases)+len(do.Repo)-len(do.Runtime))

	for _, base := range do.Bases {
		for _, pkg := range base {
			if !do.Runtime.get(pkg.Name) {
				makeOnly = append(makeOnly, pkg.Name)
			}
		}
	}

	for _, pkg := range do.Repo {
		if !do.Runtime.get(pkg.Name()) {
			makeOnly = append(makeOnly, pkg.Name())
		}
	}

	return makeOnly
}
