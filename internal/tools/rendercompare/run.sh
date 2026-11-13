#!/usr/bin/env bash
# Reproduces the pictures and numbers in docs/RENDERER_COMPARISON.md: the same
# pictures rendered as sextant and braille by this project's encode package,
# chafa and notcurses' ncplayer, captured through tmux, drawn without a font
# (rendercompare raster) and compared with the input.
#
# Needs: go, ffmpeg, chafa, ncplayer, tmux, python3, curl, unzip, shasum.
# Usage: internal/tools/rendercompare/run.sh [WORKDIR]   (default: ./rendercompare-work)
# Downloads Big Buck Bunny (121 MB) and one NASA photograph into WORKDIR.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mkdir -p "${1:-rendercompare-work}" && cd "${1:-rendercompare-work}" && pwd)
cols=96 rows=27 # 16:9 with cells twice as tall as wide

for c in go ffmpeg chafa ncplayer tmux python3 curl unzip shasum; do
	command -v "$c" >/dev/null || { echo "run.sh: $c not found" >&2; exit 1; }
done
rc=$work/rendercompare
(cd "$here" && go build -o "$rc" .)

# fetch URL SHA256 FILE: download once and check the checksum.
fetch() {
	if [[ ! -f $3 ]]; then
		curl -sSfL -o "$3.part" "$1"
		mv "$3.part" "$3"
	fi
	echo "$2  $3" | shasum -a 256 -c --quiet -
}

# Pictures, all 640×360.
pics=$work/pictures
mkdir -p "$pics"
cd "$work"
fetch https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_640x360.m4v.zip \
	7118242b6728d40c871479c5b3c0f0fb27d748089df15d7f1b469f297c74a2d6 bbb.zip
[[ -f BigBuckBunny_640x360.m4v ]] || unzip -q bbb.zip
fetch https://images-assets.nasa.gov/image/art002e021278/art002e021278~large.jpg \
	47c9d6995a07ff5e134a248bf6d5d29d5d0239b40f9c4a2c33380ced0d72467c earthset.jpg
ff() { ffmpeg -v error -nostdin -y "$@"; }
frame() { ff -ss "$2" -i BigBuckBunny_640x360.m4v -frames:v 1 -vf scale=640:360 "$pics/$1.png"; }
frame bunny 75
frame meadow 180
frame trees 465
ff -i earthset.jpg -vf crop=1920:1080:0:150,scale=640:360 "$pics/earthset.png"
ff -f lavfi -i testsrc2=s=640x360 -frames:v 1 "$pics/testsrc2.png"
ff -f lavfi -i mandelbrot=s=640x360 -frames:v 1 "$pics/mandelbrot.png"
ff -f lavfi -i color=s=640x360 -frames:v 1 \
	-vf "format=rgb24,geq=r='255*X/W':g='255*(1-Y/H)':b='128+100*sin(PI*X/W)'" "$pics/gradient.png"

# replay RAW ANS: draws a recorded output in a tmux pane of the grid's size
# (one row more, so a final newline doesn't scroll) and captures the screen.
tm() { tmux -L rendercompare -f /dev/null "$@"; }
replay() {
	tm new-session -d -s replay -x "$cols" -y $((rows + 1)) \
		"cat '$1'; tmux -L rendercompare wait-for -S drawn; sleep 600"
	tm wait-for drawn
	tm capture-pane -t replay -e -N -p -S 0 -E $((rows - 1)) >"$2"
	tm kill-session -t replay
}
trap 'tm kill-server 2>/dev/null || true' EXIT

# ncplayer, on a pseudo-terminal that answers as foot, so notcurses uses sextants.
ncplayer_run() {
	python3 -I "$here/ptyrun.py" "$1" 'foot(1.18.0)' "$rows" "$cols" \
		env TERM=xterm-256color COLORTERM=truecolor ncplayer -k -t 0 "${@:2}"
}

# render MODE SETUP TOOL INPUT RAW: one tool's output for one picture.
render() {
	local mode=$1 setup=$2 tool=$3 in=$4 raw=$5
	local chafa_sym=$mode nc_blit=$mode nc_scale=stretch
	[[ $mode == sextant ]] && nc_blit=sex
	[[ $setup == pre ]] && nc_scale=none
	case $tool in
	ours) "$rc" encode -r "$mode" -cols "$cols" -rows "$rows" "$in" >"$raw" ;;
	chafa) chafa -f symbols -c full --symbols "$chafa_sym" -s "${cols}x$rows" --stretch "$in" >"$raw" ;;
	chafa-w9) chafa -f symbols -c full --symbols "$chafa_sym" -s "${cols}x$rows" --stretch -w 9 "$in" >"$raw" ;;
	ncplayer) ncplayer_run "$raw" -b "$nc_blit" -s "$nc_scale" "$in" ;;
	esac
}

# chafa's --color-space din99d changes nothing with 24-bit color (it is for palettes).
tools=(ours chafa chafa-w9 ncplayer)
results=$work/results.tsv
printf 'mode\tsetup\tpicture\ttool\tbytes\tgrid_psnr\tgrid_ssim\tdisplay_psnr\tdisplay_ssim\n' >"$results"
dw=$((cols * 12)) dh=$((rows * 24)) # raster size: 12×24 pixels per cell

# metrics A B: PSNR and SSIM of A against B (same size).
metrics() {
	local log
	log=$(ffmpeg -v info -nostdin -i "$1" -i "$2" -lavfi "[0]split[a][b];[1]split[r][s];[a][r]psnr;[b][s]ssim" -f null - 2>&1)
	printf '%s\t%s' "$(sed -n 's/.*PSNR.*average:\([0-9.inf]*\).*/\1/p' <<<"$log")" \
		"$(sed -n 's/.*SSIM.*All:\([0-9.]*\).*/\1/p' <<<"$log")"
}

for mode in sextant braille; do
	pw=$((cols * 2)) ph=$((rows * 3))
	[[ $mode == braille ]] && ph=$((rows * 4))
	for pic in "$pics"/*.png; do
		name=$(basename "$pic" .png)
		dir=$work/$mode/$name
		mkdir -p "$dir"
		# The pre-scaled input and grid reference: the picture at the pixel grid.
		"$rc" scale -w "$pw" -h "$ph" "$pic" "$dir/ref.png"
		# The display reference: the picture at the raster's size.
		ff -i "$pic" -vf "scale=$dw:$dh:flags=lanczos" "$dir/display.png"
		for setup in pre native; do
			in=$pic
			[[ $setup == pre ]] && in=$dir/ref.png
			for tool in "${tools[@]}"; do
				out=$dir/$setup-$tool
				render "$mode" "$setup" "$tool" "$in" "$out.raw"
				replay "$out.raw" "$out.ans"
				"$rc" raster -cols "$cols" -rows "$rows" "$out.ans" "$out.png"
				grid=-$'\t'-
				if [[ $mode == sextant ]]; then
					# Back to the pixel grid: exact, since a cell is 12×24 pixels.
					ff -i "$out.png" -vf "scale=$pw:$ph:flags=area" "$out.grid.png"
					grid=$(metrics "$out.grid.png" "$dir/ref.png")
				fi
				printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$mode" "$setup" "$name" "$tool" \
					"$(wc -c <"$out.raw" | tr -d ' ')" "$grid" "$(metrics "$out.png" "$dir/display.png")" | tee -a "$results"
			done
		done
	done
done
echo "results: $results"

# The crops in docs/images/comparison: 576×324 pixels (48×13.5 cells), unscaled.
img=$work/docimg
mkdir -p "$img"
crop() { ff -i "$1" -vf "crop=576:324:$3:$4" -compression_level 100 "$img/$2.png"; }
for t in ours chafa-w9 ncplayer; do
	crop "$work/sextant/bunny/pre-$t.png" "sextant-bunny-${t%-w9}" 480 0
	crop "$work/braille/bunny/pre-$t.png" "braille-bunny-${t%-w9}" 480 0
	crop "$work/sextant/testsrc2/pre-$t.png" "sextant-testsrc2-${t%-w9}" 0 0
done
crop "$work/sextant/bunny/display.png" sextant-bunny-picture 480 0
crop "$work/sextant/testsrc2/display.png" sextant-testsrc2-picture 0 0
echo "pictures: $img"
