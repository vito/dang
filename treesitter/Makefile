targets=queries/nvim/highlights.scm \
				queries/helix/highlights.scm \
				queries/locals.scm

.PHONY: all clean install

all: $(targets)

queries/nvim/%.scm: queries/nvim/%.scm.tmpl gen/main.go go.mod go.sum
	env NEOVIM=1 go run ./gen < $< > $@

queries/helix/%.scm: queries/helix/%.scm.tmpl gen/main.go go.mod go.sum
	env HELIX=1 go run ./gen < $< > $@

queries/%.scm: queries/%.scm.tmpl gen/main.go go.mod go.sum
	env HELIX=1 go run ./gen < $< > $@

clean:
	rm -f $(targets)

install:
	cp ./queries/*.scm ../sprout.vim/queries/sprout/
	cp ./queries/nvim/*.scm ../sprout.vim/queries/sprout/
	cp ./queries/*.scm ../helix/runtime/queries/sprout/
	cp ./queries/helix/*.scm ../helix/runtime/queries/sprout/
