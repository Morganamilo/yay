package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
	//gopkg "github.com/mikkeloscar/gopkgbuild"
)


const PROVIDES = false

type target struct {
	Db      string
	Name    string
	Mod     string
	Version string
}

func toTarget(pkg string) target {
	db, dep := splitDbFromName(pkg)
	name, mod, version := splitDep(dep)

	return target{
		db,
		name,
		mod,
		version,
	}
}

func (t target) DepString() string {
	return t.Name + t.Mod + t.Version
}

func (t target) String() string {
	if t.Db != "" {
		return t.Db + "/" + t.DepString()
	}

	return t.DepString()
}

type depPool struct {
	Targets  []target
	Repo     map[string]*alpm.Package
	Aur      map[string]*rpc.Pkg
	AurCache map[string]*rpc.Pkg
	Groups   []string
	LocalDb  *alpm.Db
	SyncDb   alpm.DbList
	Warnings *aurWarnings
}

func makeDepPool() (*depPool, error) {
	localDb, err := alpmHandle.LocalDb()
	if err != nil {
		return nil, err
	}
	syncDb, err := alpmHandle.SyncDbs()
	if err != nil {
		return nil, err
	}

	dp := &depPool{
		make([]target, 0),
		make(map[string]*alpm.Package),
		make(map[string]*rpc.Pkg),
		make(map[string]*rpc.Pkg),
		make([]string, 0),
		localDb,
		syncDb,
		&aurWarnings{},
	}

	return dp, nil
}

func (dp *depPool) String() string {
	str := ""
	str += "\n" + red("Targets") + " (" + strconv.Itoa(len(dp.Targets)) + ") :"
	for _, pkg := range dp.Targets {
		str += " " + pkg.String()
	}

	str += "\n" + red("Repo") + " (" + strconv.Itoa(len(dp.Repo)) + ") :"
	for pkg := range dp.Repo {
		str += " " + pkg
	}

	str += "\n" + red("Aur") + " (" + strconv.Itoa(len(dp.Aur)) + ") :"
	for pkg := range dp.Aur {
		str += " " + pkg
	}

	str += "\n" + red("Aur Cache") + " (" + strconv.Itoa(len(dp.AurCache)) + ") :"
	for pkg := range dp.AurCache {
		str += " " + pkg
	}

	str += "\n" + red("Groups") + " (" + strconv.Itoa(len(dp.Groups)) + ") :"
	for _, pkg := range dp.Groups {
		str += " " + pkg
	}

	return str
}

// Includes db/ prefixes and group installs
func (dp *depPool) ResolveTargets(pkgs []string) error {
	for _, pkg := range pkgs {
		target := toTarget(pkg)
		dp.Targets = append(dp.Targets, target)
	}

	// RPC requests are slow
	// Combine as many AUR package requests as possible into a single RPC
	// call
	aurTargets := make(stringSet)
	var err error
	//repo := make([]*alpm.Package, 0)

	for _, target := range dp.Targets {

		// skip targets already satisfied
		// even if the user enters db/pkg and aur/pkg the latter will
		// still get skiped even if it's from a different database to
		// the one specified
		// this is how pacman behaves
		if dp.hasSatisfier(target.DepString()) {
			fmt.Println("Skipping target", target)
			continue
		}

		var foundPkg *alpm.Package
		var singleDb *alpm.Db

		// aur/ prefix means we only check the aur
		if target.Db == "aur" {
			aurTargets.set(target.DepString())
			continue
		}

		// if theres a different priefix only look in that repo
		if target.Db != "" {
			singleDb, err = alpmHandle.SyncDbByName(target.Db)
			if err != nil {
				return err
			}
			foundPkg, err = singleDb.PkgCache().FindSatisfier(target.DepString())
			//otherwise find it in any repo
		} else {
			foundPkg, err = dp.SyncDb.FindSatisfier(target.DepString())
		}

		if err == nil {
			dp.ResolveRepoDependency(foundPkg)
			continue
		} else {
			//check for groups
			//currently we dont resolve the packages in a group
			//only check if the group exists
			//would be better to check the groups from singleDb if
			//the user specified a db but theres no easy way to do
			//it without making alpm_lists so dont bother for now
			//db/group is probably a rare use case
			_, err := dp.SyncDb.PkgCachebyGroup(target.Name)

			if err == nil {
				dp.Groups = append(dp.Groups, target.String())
				continue
			}
		}

		//if there was no db prefix check the aur
		if target.Db == "" {
			aurTargets.set(target.DepString())
		}
	}

	if len(aurTargets) > 0 {
		err = dp.resolveAURPackages(aurTargets)
	}

	return nil
}

// Pseudo provides finder.
// Try to find provides by performing a search of the package name
// This effectively performs -Ss on each package
// then runs -Si on each result to cache the information.
//
// For example if you were to -S yay then yay -Ss would give:
// yay-git yay-bin yay realyog pacui pacui-git ruby-yard
// These packages will all be added to the cache incase they are needed later
// Ofcouse only the first three packages provide yay, the rest are just false
// positives.
//
// This method increases dependency resolve time
func (dp *depPool) findProvides(pkgs stringSet) error {
	var mux sync.Mutex
	var wg sync.WaitGroup

	doSearch := func(pkg string) {
		defer wg.Done()
		var err error
		var results []rpc.Pkg

		// Hack for a bigger search result, if the user wants
		// java-envronment we can search for just java instead and get
		// more hits.
		words := strings.Split(pkg, "-")

		for i := range words {
			results, err = rpc.SearchByNameDesc(strings.Join(words[:i + 1], "-"))
			if err == nil {
				break
			}
		}
		
		if err != nil {
			return
		}

		for _, result := range results {
			mux.Lock()
			if _, ok := dp.AurCache[result.Name]; !ok {
				pkgs.set(result.Name)
			}
			mux.Unlock()
		}
	}

	for pkg := range pkgs {
		wg.Add(1)
		go doSearch(pkg)
	}

	wg.Wait()

	return nil
}

func (dp *depPool) cacheAURPackages(_pkgs stringSet) error {
	pkgs := _pkgs.copy()
	query := make([]string, 0)

	for pkg := range pkgs {
		if _, ok := dp.AurCache[pkg]; ok {
			pkgs.remove(pkg)
		}
	}

	if len(pkgs) == 0 {
		return nil
	}

	//TODO: config option, maybe --deepsearh but aurman uses that flag for
	//something else already which might be confusing
	//maybe --provides
	if PROVIDES {
		err := dp.findProvides(pkgs)
		if err != nil {
			return err
		}
	}

	for pkg := range pkgs {
		if _, ok := dp.AurCache[pkg]; !ok {
			name, _, _ := splitDep(pkg)
			query = append(query, name)
		}
	}

	info, err := aurInfo(query, dp.Warnings)
	if err != nil {
		return err
	}

	for _, pkg := range info {
		// Dump everything in cache just in case we need it later
		dp.AurCache[pkg.Name] = pkg
	}

	return nil
}

func (dp *depPool) resolveAURPackages(pkgs stringSet) error {
	newPackages := make(stringSet)
	newAURPackages := make(stringSet)

	err := dp.cacheAURPackages(pkgs)
	if err != nil {
		return err
	}

	if len(pkgs) == 0 {
		return nil
	}

	for name := range pkgs {
		_, ok := dp.Aur[name]
		if ok {
			continue
		}

		pkg := dp.findSatisfierAurCache(name)
		if pkg == nil {
			continue
		}

		dp.Aur[pkg.Name] = pkg

		for _, deps := range [3][]string{pkg.Depends, pkg.MakeDepends, pkg.CheckDepends} {
			for _, dep := range deps {
				newPackages.set(dep)
			}
		}
	}

	for dep := range newPackages {
		if dp.hasSatisfier(dep) {
			continue
		}

		//has satisfier installed: skip
		_, isInstalled := dp.LocalDb.PkgCache().FindSatisfier(dep)
		if isInstalled == nil {
			continue
		}

		//has satisfier in repo: fetch it
		repoPkg, inRepos := dp.SyncDb.FindSatisfier(dep)
		if inRepos == nil {
			dp.ResolveRepoDependency(repoPkg)
			continue
		}

		//assume it's in the aur
		//ditch the versioning because the RPC cant handle it
		newAURPackages.set(dep)

	}

	err = dp.resolveAURPackages(newAURPackages)

	return err
}

func (dp *depPool) ResolveRepoDependency(pkg *alpm.Package) {
	dp.Repo[pkg.Name()] = pkg

	pkg.Depends().ForEach(func(dep alpm.Depend) (err error) {
		//have satisfier in dep tree: skip
		if dp.hasSatisfier(dep.String()) {
			return
		}

		//has satisfier installed: skip
		_, isInstalled := dp.LocalDb.PkgCache().FindSatisfier(dep.String())
		if isInstalled == nil {
			return
		}

		//has satisfier in repo: fetch it
		repoPkg, inRepos := dp.SyncDb.FindSatisfier(dep.String())
		if inRepos != nil {
			return
		}

		dp.ResolveRepoDependency(repoPkg)

		return nil
	})

}

func (dp *depPool) queryAUR(pkgs []string) error {
	_, err := aurInfo(pkgs, dp.Warnings)
	if err != nil {
		return err
	}

	return nil
}

func getDepPool(pkgs []string) (*depPool, error) {
	dp, err := makeDepPool()
	if err != nil {
		return nil, err
	}

	err = dp.ResolveTargets(pkgs)

	return dp, err
}

func (dp *depPool) findSatisfierAur(dep string) *rpc.Pkg {
	for _, pkg := range dp.Aur {
		if pkgSatisfies(pkg.Name, pkg.Version, dep) {
			return pkg
		}

		for _, provide := range pkg.Provides {
			if provideSatisfies(provide, dep) {
				return pkg
			}
		}
	}

	return nil
}

// This is mostly used to promote packages from the cache
// to the Install list
// Provide a pacman style provider menu if theres more than one candidate
// TODO: maybe intermix repo providers in the menu
func (dp *depPool) findSatisfierAurCache(dep string) *rpc.Pkg {

	pkg, err := dp.LocalDb.PkgCache().FindSatisfier(dep)
	if err == nil {
		if provider, ok := dp.AurCache[pkg.Name()]; ok {
			return provider
		}
	}

	//try to match providers
	providers := make([]*rpc.Pkg, 0)
	for _, pkg := range dp.AurCache {
		if pkgSatisfies(pkg.Name, pkg.Version, dep) {
			providers = append(providers, pkg)
		}

		for _, provide := range pkg.Provides {
			if provideSatisfies(provide, dep) {
				providers = append(providers, pkg)
			}
		}
	}

	if len(providers) == 1 {
		return providers[0]
	}

	if len(providers) > 1 {
		return providerMenu(dep, providers)
	}

	return nil
}

func (dp *depPool) findSatisfierRepo(dep string) *alpm.Package {
	for _, pkg := range dp.Repo {
		if pkgSatisfies(pkg.Name(), pkg.Version(), dep) {
			return pkg
		}

		if pkg.Provides().ForEach(func(provide alpm.Depend) error {
			if provideSatisfies(provide.String(), dep) {
				return fmt.Errorf("")
			}

			return nil
		}) != nil {
			return pkg
		}
	}

	return nil
}

func (dp *depPool) hasSatisfier(dep string) bool {
	return dp.findSatisfierRepo(dep) != nil || dp.findSatisfierAur(dep) != nil
}

func (dp *depPool) hasPackage(name string) bool {
	for _, pkg := range dp.Repo {
		if pkg.Name() == name {
			return true
		}
	}

	for _, pkg := range dp.Aur {
		if pkg.Name == name {
			return true
		}
	}

	for _, pkg := range dp.Groups {
		if pkg == name {
			return true
		}
	}

	return false
}

type missing struct {
	Good stringSet
	Missing map[string][][]string
}

func (dp *depPool) CheckMissing() (map[string][][]string) {
	missing := &missing {
		make(stringSet),
		make(map[string][][]string),
	}

	for _, target := range dp.Targets {
		dp._checkMissing(target.DepString(), make([]string, 0), missing,)
	}

	return missing.Missing
}


func (dp *depPool) _checkMissing(dep string, stack []string, missing *missing) {
	if _, err := dp.LocalDb.PkgCache().FindSatisfier(dep); err == nil {
		missing.Good.set(dep)
		return
	}

	if missing.Good.get(dep) {
		return
	}

	if trees, ok := missing.Missing[dep]; ok {
		for _, tree := range trees {
			if stringSliceEqual(tree, stack) {
				return
			}
		}
		missing.Missing[dep] = append(missing.Missing[dep], stack)
		return
	}

	aurPkg := dp.findSatisfierAur(dep)
	if aurPkg != nil {
		missing.Good.set(dep)
		for _, deps := range [3][]string{aurPkg.Depends, aurPkg.MakeDepends, aurPkg.CheckDepends} {
			for _, aurDep := range deps {
				dp._checkMissing(aurDep, append(stack, aurPkg.Name), missing)
			}
		}

		return
	}

	repoPkg := dp.findSatisfierRepo(dep)
	if repoPkg != nil {
		missing.Good.set(dep)
		repoPkg.Depends().ForEach(func(repoDep alpm.Depend) error {
			dp._checkMissing(repoDep.String(), append(stack, repoPkg.Name()), missing)
			return nil
		})

		return
	}

	missing.Missing[dep] = [][]string{stack}
}
