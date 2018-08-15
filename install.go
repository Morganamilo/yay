package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	gosrc "github.com/Morganamilo/go-srcinfo"
	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
)

// Install handles package installs
func install(parser *arguments) error {
	var err error
	var incompatible stringSet
	var do *depOrder

	var aurUp upSlice
	var repoUp upSlice

	warnings := &aurWarnings{}

	removeMake := false
	srcinfosStale := make(map[string]*gosrc.Srcinfo)

	if mode == ModeAny || mode == ModeRepo {
		if config.CombinedUpgrade {
			if parser.existsArg("y", "refresh") {
				err = earlyRefresh(parser)
				if err != nil {
					return fmt.Errorf("Error refreshing databases")
				}
			}
		} else if parser.existsArg("y", "refresh") || parser.existsArg("u", "sysupgrade") || len(parser.targets) > 0 {
			err = earlyPacmanCall(parser)
			if err != nil {
				return err
			}
		}
	}

	//we may have done -Sy, our handle now has an old
	//database.
	err = initAlpmHandle()
	if err != nil {
		return err
	}

	_, _, localNames, remoteNames, err := filterPackages()
	if err != nil {
		return err
	}

	remoteNamesCache := sliceToStringSet(remoteNames)
	localNamesCache := sliceToStringSet(localNames)

	requestTargets := parser.copy().targets

	//create the arguments to pass for the repo install
	arguments := parser.copy()
	arguments.delArg("asdeps", "asdep")
	arguments.delArg("asexplicit", "asexp")
	arguments.op = "S"
	arguments.clearTargets()

	if mode == ModeAUR {
		arguments.delArg("u", "sysupgrade")
	}

	//if we are doing -u also request all packages needing update
	if parser.existsArg("u", "sysupgrade") {
		aurUp, repoUp, err = upList(warnings)
		if err != nil {
			return err
		}

		warnings.print()

		ignore, aurUp, err := upgradePkgs(aurUp, repoUp)
		if err != nil {
			return err
		}

		for _, up := range repoUp {
			if !ignore.get(up.Name) {
				requestTargets = append(requestTargets, up.Name)
				parser.addTarget(up.Name)
			}
		}

		for up := range aurUp {
			requestTargets = append(requestTargets, "aur/"+up)
			parser.addTarget("aur/" + up)
		}

		value, _, exists := cmdArgs.getArg("ignore")

		if len(ignore) > 0 {
			ignoreStr := strings.Join(ignore.toSlice(), ",")
			if exists {
				ignoreStr += "," + value
			}
			arguments.options["ignore"] = ignoreStr
		}
	}

	targets := sliceToStringSet(parser.targets)

	dp, err := getDepPool(requestTargets, warnings)
	if err != nil {
		return err
	}

	err = dp.CheckMissing()
	if err != nil {
		return err
	}

	if len(dp.Aur) == 0 {
		if !config.CombinedUpgrade {
			if parser.existsArg("u", "sysupgrade") {
				fmt.Println(" there is nothing to do")
			}
			return nil
		}

		parser.op = "S"
		parser.delArg("y", "refresh")
		parser.options["ignore"] = arguments.options["ignore"]
		return show(passToPacman(parser))
	}

	if len(dp.Aur) > 0 && 0 == os.Geteuid() {
		return fmt.Errorf(bold(red(arrow)) + " Refusing to install AUR Packages as root, Aborting.")
	}

	conflicts, err := dp.CheckConflicts()
	if err != nil {
		return err
	}

	do = getDepOrder(dp)
	if err != nil {
		return err
	}

	for _, pkg := range do.Repo {
		arguments.addTarget(pkg.DB().Name() + "/" + pkg.Name())
	}

	for _, pkg := range dp.Groups {
		arguments.addTarget(pkg)
	}

	if len(do.Aur) == 0 && len(arguments.targets) == 0 && (!parser.existsArg("u", "sysupgrade") || mode == ModeAUR) {
		fmt.Println(" there is nothing to do")
		return nil
	}

	do.Print()
	fmt.Println()

	if do.HasMake() {
		if config.RemoveMake == "yes" {
			removeMake = true
		} else if config.RemoveMake == "no" {
			removeMake = false
		} else if continueTask("Remove make dependencies after install?", false) {
			removeMake = true
		}
	}

	if config.CleanMenu {
		askClean := pkgbuildNumberMenu(do.Aur, do.Bases, remoteNamesCache)
		toClean, err := cleanNumberMenu(do.Aur, do.Bases, remoteNamesCache, askClean)
		if err != nil {
			return err
		}

		cleanBuilds(toClean)
	}

	toSkip := pkgBuildsToSkip(do.Aur, do.Bases, targets)
	cloned, err := downloadPkgBuilds(do.Aur, do.Bases, toSkip)
	if err != nil {
		return err
	}

	var toDiff []string
	var toEdit []string

	if config.DiffMenu {
		pkgbuildNumberMenu(do.Aur, do.Bases, remoteNamesCache)
		toDiff, err = diffNumberMenu(do.Aur, do.Bases, remoteNamesCache)
		if err != nil {
			return err
		}

		if len(toDiff) > 0 {
			err = showPkgBuildDiffs(toDiff, do.Bases, cloned)
			if err != nil {
				return err
			}
		}
	}

	if len(toDiff) > 0 {
		oldValue := config.NoConfirm
		config.NoConfirm = false
		fmt.Println()
		if !continueTask(bold(green("Proceed with install?")), true) {
			return fmt.Errorf("Aborting due to user")
		}
		config.NoConfirm = oldValue
	}

	err = mergePkgBuilds(do.Aur)
	if err != nil {
		return err
	}

	//initial srcinfo parse before pkgver() bump
	err = parseSRCINFOFiles(do.Aur, srcinfosStale, do.Bases)
	if err != nil {
		return err
	}

	if config.EditMenu {
		pkgbuildNumberMenu(do.Aur, do.Bases, remoteNamesCache)
		toEdit, err = editNumberMenu(do.Aur, do.Bases, remoteNamesCache)
		if err != nil {
			return err
		}

		if len(toEdit) > 0 {
			err = editPkgBuilds(toEdit, srcinfosStale)
			if err != nil {
				return err
			}
		}
	}

	if len(toEdit) > 0 {
		oldValue := config.NoConfirm
		config.NoConfirm = false
		fmt.Println()
		if !continueTask(bold(green("Proceed with install?")), true) {
			return fmt.Errorf("Aborting due to user")
		}
		config.NoConfirm = oldValue
	}

	incompatible, err = getIncompatible(do.Aur, srcinfosStale)
	if err != nil {
		return err
	}

	if config.PGPFetch {
		err = checkPgpKeys(do.Aur, do.Bases, srcinfosStale)
		if err != nil {
			return err
		}
	}

	if !config.CombinedUpgrade {
		arguments.delArg("u", "sysupgrade")
	}

	if len(arguments.targets) > 0 || arguments.existsArg("u") {
		err := show(passToPacman(arguments))
		if err != nil {
			return fmt.Errorf("Error installing repo packages")
		}

		depArguments := makeArguments()
		depArguments.addArg("D", "asdeps")
		expArguments := makeArguments()
		expArguments.addArg("D", "asexplicit")

		for _, pkg := range do.Repo {
			if !dp.Explicit.get(pkg.Name()) && !localNamesCache.get(pkg.Name()) && !remoteNamesCache.get(pkg.Name()) {
				depArguments.addTarget(pkg.Name())
				continue
			}

			if parser.existsArg("asdeps", "asdep") && dp.Explicit.get(pkg.Name()) {
				depArguments.addTarget(pkg.Name())
			} else if parser.existsArg("asexp", "asexplicit") && dp.Explicit.get(pkg.Name()) {
				expArguments.addTarget(pkg.Name())
			}
		}

		if len(depArguments.targets) > 0 {
			_, stderr, err := capture(passToPacman(depArguments))
			if err != nil {
				return fmt.Errorf("%s%s", stderr, err)
			}
		}

		if len(expArguments.targets) > 0 {
			_, stderr, err := capture(passToPacman(expArguments))
			if err != nil {
				return fmt.Errorf("%s%s", stderr, err)
			}
		}
	}

	go updateCompletion(false)

	err = downloadPkgBuildsSources(do.Aur, do.Bases, incompatible)
	if err != nil {
		return err
	}

	err = buildInstallPkgBuilds(dp, do, srcinfosStale, parser, incompatible, conflicts)
	if err != nil {
		return err
	}

	if removeMake {
		removeArguments := makeArguments()
		removeArguments.addArg("R", "u")

		for _, pkg := range do.getMake() {
			removeArguments.addTarget(pkg)
		}

		oldValue := config.NoConfirm
		config.NoConfirm = true
		err = show(passToPacman(removeArguments))
		config.NoConfirm = oldValue

		if err != nil {
			return err
		}
	}

	if config.CleanAfter {
		clean(do.Aur)
	}

	return nil
}

func inRepos(syncDb alpm.DbList, pkg string) bool {
	target := toTarget(pkg)

	if target.Db == "aur" {
		return false
	} else if target.Db != "" {
		return true
	}

	_, err := syncDb.FindSatisfier(target.DepString())
	if err == nil {
		return true
	}

	_, err = syncDb.PkgCachebyGroup(target.Name)
	if err == nil {
		return true
	}

	return false
}

func earlyPacmanCall(parser *arguments) error {
	arguments := parser.copy()
	arguments.op = "S"
	targets := parser.targets
	parser.clearTargets()
	arguments.clearTargets()

	syncDb, err := alpmHandle.SyncDbs()
	if err != nil {
		return err
	}

	if mode == ModeRepo {
		arguments.targets = targets
	} else {
		alpmHandle.SetQuestionCallback(func(alpm.QuestionAny) {})
		//seperate aur and repo targets
		for _, target := range targets {
			if inRepos(syncDb, target) {
				arguments.addTarget(target)
			} else {
				parser.addTarget(target)
			}
		}
	}

	if parser.existsArg("y", "refresh") || parser.existsArg("u", "sysupgrade") || len(arguments.targets) > 0 {
		err = show(passToPacman(arguments))
		if err != nil {
			return fmt.Errorf("Error installing repo packages")
		}
	}

	return nil
}

func earlyRefresh(parser *arguments) error {
	arguments := parser.copy()
	parser.delArg("y", "refresh")
	arguments.delArg("u", "sysupgrade")
	arguments.delArg("s", "search")
	arguments.delArg("i", "info")
	arguments.delArg("l", "list")
	arguments.clearTargets()
	return show(passToPacman(arguments))
}

func getIncompatible(pkgs []string, srcinfos map[string]*gosrc.Srcinfo) (stringSet, error) {
	incompatible := make(stringSet)
	alpmArch, err := alpmHandle.Arch()
	if err != nil {
		return nil, err
	}

nextpkg:
	for _, pkg := range pkgs {
		for _, arch := range srcinfos[pkg].Arch {
			if arch == "any" || arch == alpmArch {
				continue nextpkg
			}
		}

		incompatible.set(pkg)
	}

	if len(incompatible) > 0 {
		fmt.Println()
		fmt.Print(bold(yellow(arrow)) + " The following packages are not compatible with your architecture:")
		for pkg := range incompatible {
			fmt.Print("  " + cyan(pkg))
		}

		fmt.Println()

		if !continueTask("Try to build them anyway?", true) {
			return nil, fmt.Errorf("Aborting due to user")
		}
	}

	return incompatible, nil
}

func parsePackageList(dir string) (map[string]string, string, error) {
	stdout, stderr, err := capture(passToMakepkg(dir, "--packagelist"))

	if err != nil {
		return nil, "", fmt.Errorf("%s%s", stderr, err)
	}

	var version string
	lines := strings.Split(stdout, "\n")
	pkgdests := make(map[string]string)

	for _, line := range lines {
		if line == "" {
			continue
		}

		fileName := filepath.Base(line)
		split := strings.Split(fileName, "-")

		if len(split) < 4 {
			return nil, "", fmt.Errorf("Can not find package name : %s", split)
		}

		// pkgname-pkgver-pkgrel-arch.pkgext
		// This assumes 3 dashes after the pkgname, Will cause an error
		// if the PKGEXT contains a dash. Please no one do that.
		pkgname := strings.Join(split[:len(split)-3], "-")
		version = strings.Join(split[len(split)-3:len(split)-2], "-")
		pkgdests[pkgname] = line
	}

	return pkgdests, version, nil
}

func pkgbuildNumberMenu(pkgs []string, bases map[string][]*rpc.Pkg, installed stringSet) bool {
	toPrint := ""
	askClean := false

	for n, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)

		toPrint += fmt.Sprintf(magenta("%3d")+" %-40s", len(pkgs)-n,
			bold(formatPkgbase(bases[pkg])))

		anyInstalled := false
		for _, base := range bases[pkg] {
			anyInstalled = anyInstalled || installed.get(base.Name)
		}

		if anyInstalled {
			toPrint += bold(green(" (Installed)"))
		}

		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			toPrint += bold(green(" (Build Files Exist)"))
			askClean = true
		}

		toPrint += "\n"
	}

	fmt.Print(toPrint)

	return askClean
}

func cleanNumberMenu(pkgs []string, bases map[string][]*rpc.Pkg, installed stringSet, hasClean bool) ([]string, error) {
	toClean := make([]string, 0)

	if !hasClean {
		return toClean, nil
	}

	fmt.Println(bold(green(arrow + " Packages to cleanBuild?")))
	fmt.Println(bold(green(arrow) + cyan(" [N]one ") + "[A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)"))
	fmt.Print(bold(green(arrow + " ")))
	cleanInput, err := getInput(config.AnswerClean)
	if err != nil {
		return nil, err
	}

	cInclude, cExclude, cOtherInclude, cOtherExclude := parseNumberMenu(cleanInput)
	cIsInclude := len(cExclude) == 0 && len(cOtherExclude) == 0

	if cOtherInclude.get("abort") || cOtherInclude.get("ab") {
		return nil, fmt.Errorf("Aborting due to user")
	}

	if !cOtherInclude.get("n") && !cOtherInclude.get("none") {
		for i, pkg := range pkgs {
			anyInstalled := false
			for _, base := range bases[pkg] {
				anyInstalled = anyInstalled || installed.get(base.Name)
			}

			dir := filepath.Join(config.BuildDir, pkg)
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				continue
			}

			if !cIsInclude && cExclude.get(len(pkgs)-i) {
				continue
			}

			if anyInstalled && (cOtherInclude.get("i") || cOtherInclude.get("installed")) {
				toClean = append(toClean, pkg)
				continue
			}

			if !anyInstalled && (cOtherInclude.get("no") || cOtherInclude.get("notinstalled")) {
				toClean = append(toClean, pkg)
				continue
			}

			if cOtherInclude.get("a") || cOtherInclude.get("all") {
				toClean = append(toClean, pkg)
				continue
			}

			if cIsInclude && (cInclude.get(len(pkgs)-i) || cOtherInclude.get(pkg)) {
				toClean = append(toClean, pkg)
				continue
			}

			if !cIsInclude && (!cExclude.get(len(pkgs)-i) && !cOtherExclude.get(pkg)) {
				toClean = append(toClean, pkg)
				continue
			}
		}
	}

	return toClean, nil
}

func editNumberMenu(pkgs []string, bases map[string][]*rpc.Pkg, installed stringSet) ([]string, error) {
	return editDiffNumberMenu(pkgs, bases, installed, false)
}

func diffNumberMenu(pkgs []string, bases map[string][]*rpc.Pkg, installed stringSet) ([]string, error) {
	return editDiffNumberMenu(pkgs, bases, installed, true)
}

func editDiffNumberMenu(pkgs []string, bases map[string][]*rpc.Pkg, installed stringSet, diff bool) ([]string, error) {
	toEdit := make([]string, 0)
	var editInput string
	var err error

	fmt.Println(bold(green(arrow) + cyan(" [N]one ") + "[A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)"))

	if diff {
		fmt.Println(bold(green(arrow + " Diffs to show?")))
		fmt.Print(bold(green(arrow + " ")))
		editInput, err = getInput(config.AnswerDiff)
		if err != nil {
			return nil, err
		}
	} else {
		fmt.Println(bold(green(arrow + " PKGBUILDs to edit?")))
		fmt.Print(bold(green(arrow + " ")))
		editInput, err = getInput(config.AnswerEdit)
		if err != nil {
			return nil, err
		}
	}

	eInclude, eExclude, eOtherInclude, eOtherExclude := parseNumberMenu(editInput)
	eIsInclude := len(eExclude) == 0 && len(eOtherExclude) == 0

	if eOtherInclude.get("abort") || eOtherInclude.get("ab") {
		return nil, fmt.Errorf("Aborting due to user")
	}

	if !eOtherInclude.get("n") && !eOtherInclude.get("none") {
		for i, pkg := range pkgs {
			anyInstalled := false
			for _, base := range bases[pkg] {
				anyInstalled = anyInstalled || installed.get(base.Name)
			}

			if !eIsInclude && eExclude.get(len(pkgs)-i) {
				continue
			}

			if anyInstalled && (eOtherInclude.get("i") || eOtherInclude.get("installed")) {
				toEdit = append(toEdit, pkg)
				continue
			}

			if !anyInstalled && (eOtherInclude.get("no") || eOtherInclude.get("notinstalled")) {
				toEdit = append(toEdit, pkg)
				continue
			}

			if eOtherInclude.get("a") || eOtherInclude.get("all") {
				toEdit = append(toEdit, pkg)
				continue
			}

			if eIsInclude && (eInclude.get(len(pkgs)-i) || eOtherInclude.get(pkg)) {
				toEdit = append(toEdit, pkg)
			}

			if !eIsInclude && (!eExclude.get(len(pkgs)-i) && !eOtherExclude.get(pkg)) {
				toEdit = append(toEdit, pkg)
			}
		}
	}

	return toEdit, nil
}

func cleanBuilds(pkgs []string) {
	for i, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)
		fmt.Printf(bold(cyan("::")+" Deleting (%d/%d): %s\n"), i+1, len(pkgs), cyan(dir))
		os.RemoveAll(dir)
	}
}

func showPkgBuildDiffs(pkgs []string, bases map[string][]*rpc.Pkg, cloned stringSet) error {
	for _, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)
		if shouldUseGit(dir) {
			start := "HEAD"

			if cloned.get(pkg) {
				start = gitEmptyTree
			} else {
				hasDiff, err := gitHasDiff(config.BuildDir, pkg)
				if err != nil {
					return err
				}

				if !hasDiff {
					fmt.Printf("%s %s: %s\n", bold(yellow(arrow)), cyan(formatPkgbase(bases[pkg])), bold("No changes -- skipping"))
					continue
				}
			}

			args := []string{"diff", start + "..HEAD@{upstream}", "--src-prefix", dir + "/", "--dst-prefix", dir + "/", "--", ".", ":(exclude).SRCINFO"}
			if useColor {
				args = append(args, "--color=always")
			} else {
				args = append(args, "--color=never")
			}
			err := show(passToGit(dir, args...))
			if err != nil {
				return err
			}
		} else {
			args := []string{"diff"}
			if useColor {
				args = append(args, "--color=always")
			} else {
				args = append(args, "--color=never")
			}
			args = append(args, "--no-index", "/var/empty", dir)
			// git always returns 1. why? I have no idea
			show(passToGit(dir, args...))
		}
	}

	return nil
}

func editPkgBuilds(pkgs []string, srcinfos map[string]*gosrc.Srcinfo) error {
	pkgbuilds := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)
		pkgbuilds = append(pkgbuilds, filepath.Join(dir, "PKGBUILD"))

		for _, splitPkg := range srcinfos[pkg].SplitPackages() {
			if splitPkg.Install != "" {
				pkgbuilds = append(pkgbuilds, filepath.Join(dir, splitPkg.Install))
			}
		}
	}

	if len(pkgbuilds) > 0 {
		editor, editorArgs := editor()
		editorArgs = append(editorArgs, pkgbuilds...)
		editcmd := exec.Command(editor, editorArgs...)
		editcmd.Stdin, editcmd.Stdout, editcmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := editcmd.Run()
		if err != nil {
			return fmt.Errorf("Editor did not exit successfully, Aborting: %s", err)
		}
	}

	return nil
}

func parseSRCINFOFiles(pkgs []string, srcinfos map[string]*gosrc.Srcinfo, bases map[string][]*rpc.Pkg) error {
	for k, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)

		str := bold(cyan("::") + " Parsing SRCINFO (%d/%d): %s\n")
		fmt.Printf(str, k+1, len(pkgs), cyan(formatPkgbase(bases[pkg])))

		pkgbuild, err := gosrc.ParseFile(filepath.Join(dir, ".SRCINFO"))
		if err != nil {
			return fmt.Errorf("%s: %s", formatPkgbase(bases[pkg]), err)
		}

		srcinfos[pkg] = pkgbuild
	}

	return nil
}

func tryParsesrcinfosFile(pkgs []string, srcinfos map[string]*gosrc.Srcinfo, bases map[string][]*rpc.Pkg) {
	for k, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)

		str := bold(cyan("::") + " Parsing SRCINFO (%d/%d): %s\n")
		fmt.Printf(str, k+1, len(pkgs), cyan(formatPkgbase(bases[pkg])))

		pkgbuild, err := gosrc.ParseFile(filepath.Join(dir, ".SRCINFO"))
		if err != nil {
			fmt.Printf("cannot parse %s skipping: %s\n", formatPkgbase(bases[pkg]), err)
			continue
		}

		srcinfos[pkg] = pkgbuild
	}
}

func pkgBuildsToSkip(pkgs []string, bases map[string][]*rpc.Pkg, targets stringSet) stringSet {
	toSkip := make(stringSet)

	for _, pkg := range pkgs {
		isTarget := false
		for _, base := range bases[pkg] {
			isTarget = isTarget || targets.get(base.Name)
		}

		if config.ReDownload == "no" || (config.ReDownload == "yes" && isTarget) {
			dir := filepath.Join(config.BuildDir, pkg, ".SRCINFO")
			pkgbuild, err := gosrc.ParseFile(dir)

			if err == nil {
				if alpm.VerCmp(pkgbuild.Version(), bases[pkg][0].Version) >= 0 {
					toSkip.set(pkg)
				}
			}
		}
	}

	return toSkip
}

func mergePkgBuilds(pkgs []string) error {
	for _, pkg := range pkgs {
		if shouldUseGit(filepath.Join(config.BuildDir, pkg)) {
			err := gitMerge(config.BuildDir, pkg)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func downloadPkgBuilds(pkgs []string, bases map[string][]*rpc.Pkg, toSkip stringSet) (stringSet, error) {
	cloned := make(stringSet)
	downloaded := 0
	var wg sync.WaitGroup
	var mux sync.Mutex
	var errs MultiError

	download := func(k int, pkg string) {
		defer wg.Done()

		if toSkip.get(pkg) {
			mux.Lock()
			downloaded++
			str := bold(cyan("::") + " PKGBUILD up to date, Skipping (%d/%d): %s\n")
			fmt.Printf(str, downloaded, len(pkgs), cyan(formatPkgbase(bases[pkg])))
			mux.Unlock()
			return
		}

		if shouldUseGit(filepath.Join(config.BuildDir, pkg)) {
			clone, err := gitDownload(baseURL+"/"+pkg+".git", config.BuildDir, pkg)
			if err != nil {
				errs.Add(err)
				return
			}
			if clone {
				mux.Lock()
				cloned.set(pkg)
				mux.Unlock()
			}
		} else {
			err := downloadAndUnpack(baseURL+bases[pkg][0].URLPath, config.BuildDir)
			if err != nil {
				errs.Add(err)
				return
			}
		}

		mux.Lock()
		downloaded++
		str := bold(cyan("::") + " Downloaded PKGBUILD (%d/%d): %s\n")
		fmt.Printf(str, downloaded, len(pkgs), cyan(formatPkgbase(bases[pkg])))
		mux.Unlock()
	}

	for k, pkg := range pkgs {
		wg.Add(1)
		go download(k, pkg)
	}

	wg.Wait()

	return cloned, errs.Return()
}

func downloadPkgBuildsSources(pkgs []string, bases map[string][]*rpc.Pkg, incompatible stringSet) (err error) {
	for _, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)
		args := []string{"--verifysource", "-Ccf"}

		if incompatible.get(pkg) {
			args = append(args, "--ignorearch")
		}

		err = show(passToMakepkg(dir, args...))
		if err != nil {
			return fmt.Errorf("Error downloading sources: %s", cyan(formatPkgbase(bases[pkg])))
		}
	}

	return
}

func buildInstallPkgBuilds(dp *depPool, do *depOrder, srcinfos map[string]*gosrc.Srcinfo, parser *arguments, incompatible stringSet, conflicts mapStringSet) error {
	for _, pkg := range do.Aur {
		dir := filepath.Join(config.BuildDir, pkg)
		built := true

		srcinfo := srcinfos[pkg]

		args := []string{"--nobuild", "-fC"}

		if incompatible.get(pkg) {
			args = append(args, "--ignorearch")
		}

		//pkgver bump
		err := show(passToMakepkg(dir, args...))
		if err != nil {
			return fmt.Errorf("Error making: %s", formatPkgbase(do.Bases[pkg]))
		}

		pkgdests, version, err := parsePackageList(dir)
		if err != nil {
			return err
		}

		isExplicit := false
		for _, base := range do.Bases[pkg] {
			isExplicit = isExplicit || dp.Explicit.get(base.Name)
		}
		if config.ReBuild == "no" || (config.ReBuild == "yes" && isExplicit) {
			for _, split := range do.Bases[pkg] {
				pkgdest, ok := pkgdests[split.Name]
				if !ok {
					return fmt.Errorf("Could not find PKGDEST for: %s", split.Name)
				}

				_, err := os.Stat(pkgdest)
				if os.IsNotExist(err) {
					built = false
				} else if err != nil {
					return err
				}
			}
		} else {
			built = false
		}

		if built {
			fmt.Println(bold(yellow(arrow)),
				cyan(pkg+"-"+version)+bold(" Already made -- skipping build"))
		} else {
			args := []string{"-cf", "--noconfirm", "--noextract", "--noprepare", "--holdver"}

			if incompatible.get(pkg) {
				args = append(args, "--ignorearch")
			}

			err := show(passToMakepkg(dir, args...))
			if err != nil {
				return fmt.Errorf("Error making: %s", formatPkgbase(do.Bases[pkg]))
			}
		}

		arguments := parser.copy()
		arguments.clearTargets()
		arguments.op = "U"
		arguments.delArg("confirm")
		arguments.delArg("noconfirm")
		arguments.delArg("c", "clean")
		arguments.delArg("q", "quiet")
		arguments.delArg("q", "quiet")
		arguments.delArg("y", "refresh")
		arguments.delArg("u", "sysupgrade")
		arguments.delArg("w", "downloadonly")

		oldConfirm := config.NoConfirm

		//conflicts have been checked so answer y for them
		if config.UseAsk {
			ask, _ := strconv.Atoi(cmdArgs.globals["ask"])
			uask := alpm.QuestionType(ask) | alpm.QuestionTypeConflictPkg
			cmdArgs.globals["ask"] = fmt.Sprint(uask)
		} else {
			conflict := false
			for _, split := range do.Bases[pkg] {
				if _, ok := conflicts[split.Name]; ok {
					conflict = true
				}
			}

			if !conflict {
				config.NoConfirm = true
			}
		}

		depArguments := makeArguments()
		depArguments.addArg("D", "asdeps")
		expArguments := makeArguments()
		expArguments.addArg("D", "asexplicit")

		//remotenames: names of all non repo packages on the system
		_, _, localNames, remoteNames, err := filterPackages()
		if err != nil {
			return err
		}

		//cache as a stringset. maybe make it return a string set in the first
		//place
		remoteNamesCache := sliceToStringSet(remoteNames)
		localNamesCache := sliceToStringSet(localNames)

		for _, split := range do.Bases[pkg] {
			pkgdest, ok := pkgdests[split.Name]
			if !ok {
				return fmt.Errorf("Could not find PKGDEST for: %s", split.Name)
			}

			arguments.addTarget(pkgdest)
			if !dp.Explicit.get(split.Name) && !localNamesCache.get(split.Name) && !remoteNamesCache.get(split.Name) {
				depArguments.addTarget(split.Name)
			}

			if dp.Explicit.get(split.Name) {
				if parser.existsArg("asdeps", "asdep") {
					depArguments.addTarget(split.Name)
				} else if parser.existsArg("asexplicit", "asexp") {
					expArguments.addTarget(split.Name)
				}
			}
		}

		err = show(passToPacman(arguments))
		if err != nil {
			return err
		}

		var mux sync.Mutex
		var wg sync.WaitGroup
		for _, pkg := range do.Bases[pkg] {
			wg.Add(1)
			go updateVCSData(pkg.Name, srcinfo.Source, &mux, &wg)
		}

		wg.Wait()

		err = saveVCSInfo()
		if err != nil {
			fmt.Println(err)
		}

		if len(depArguments.targets) > 0 {
			_, stderr, err := capture(passToPacman(depArguments))
			if err != nil {
				return fmt.Errorf("%s%s", stderr, err)
			}
		}
		config.NoConfirm = oldConfirm
	}

	return nil
}

func clean(pkgs []string) {
	for _, pkg := range pkgs {
		dir := filepath.Join(config.BuildDir, pkg)

		fmt.Println(bold(green(arrow +
			" CleanAfter enabled. Deleting " + pkg + " source folder.")))
		os.RemoveAll(dir)
	}
}
