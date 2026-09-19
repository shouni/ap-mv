package prompt

import (
	"fmt"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// このファイルは Google Flow Music の MV 作成へ貼る本文を組み立てます。
//
// Flow Music は曲の音源とテキスト 1 本（と任意の参照画像）を受け取り、曲全体の MV を
// 作ります。Veo のように 1 カット 1 リクエストではないので、Veo へ送るカット別本文
// （step/video_gen_prompt.go）はそのままでは貼れません。前提にしている入力（開始画像・
// 前の動画）が Flow Music には無く、全カットを並べると同じキャラクター描写がカット数ぶん
// 繰り返されるためです。ここでは同じレシピを、曲全体の演出メモ 1 本へ並べ直します。

// sharedDescriptionMinRunes は、カット間で共通する描写を 1 回にまとめる下限の長さです。
// これより短い共通部分は「a girl with」のような偶然の一致でありうるので、まとめません。
const sharedDescriptionMinRunes = 120

// FlowMusicBrief は、レシピを Flow Music へ貼る演出メモにします。
//
// visual_anchor は台本生成の時点でキャラクターの外見を毎カット書き込んでいます（Veo は
// カットごとに独立したリクエストで、前のカットの描写を知らないため）。1 本のメモでは
// それが丸ごと重複になるので、同じキャラクターのカットに共通する描写を冒頭へ 1 回だけ
// 出し、各ショットではキャラクター名に置き換えます。
func FlowMusicBrief(recipe *video.Recipe, characters *characterkit.Characters) string {
	if recipe == nil {
		return ""
	}
	music := recipe.MusicRecipe
	title := firstNonEmpty(music.Title, recipe.ProjectTitle)

	var b strings.Builder
	if title != "" {
		fmt.Fprintf(&b, "Create a music video for the attached song %q.\n", title)
	} else {
		b.WriteString("Create a music video for the attached song.\n")
	}
	b.WriteString("Follow the shot list below; its times are positions in the song.\n\n")

	writeBriefLine(&b, "Concept", recipe.Description)
	writeBriefLine(&b, "Theme", music.Theme)
	writeBriefLine(&b, "Mood", music.Mood)
	if music.Tempo > 0 {
		writeBriefLine(&b, "Tempo", fmt.Sprintf("%d BPM", music.Tempo))
	}
	writeBriefLine(&b, "Aspect ratio", recipe.AspectRatio)
	writeBriefLine(&b, "Setting", recipe.LocationAnchor)

	anchors := make([]string, len(recipe.Cuts))
	for i, cut := range recipe.Cuts {
		anchors[i] = strings.TrimSpace(cut.VisualAnchor)
	}
	for _, id := range characterIDsInOrder(recipe.Cuts) {
		name := characterName(id, characters)
		description := foldSharedDescription(recipe.Cuts, anchors, id, name)
		if description == "" {
			continue
		}
		fmt.Fprintf(&b, "Character %s: %s. Keep this appearance, outfit and art style identical in every shot.\n", name, description)
	}

	b.WriteString("\nShot list:\n")
	for i, cut := range recipe.Cuts {
		heading := fmt.Sprintf("[%s - %s]", formatTimestamp(cut.StartSec), formatTimestamp(cut.EndSec))
		if section := sectionName(recipe, cut.SectionIndex); section != "" {
			heading += " " + section
		}
		fmt.Fprintf(&b, "%s: %s\n", heading, anchors[i])
		if cue := strings.TrimSpace(cut.AudioCue); cue != "" {
			fmt.Fprintf(&b, "  Music: %s\n", cue)
		}
		if lyrics := lyricLines(cut.Dialogue); len(lyrics) > 0 && !strings.Contains(cut.AudioCue, lyrics[0]) {
			fmt.Fprintf(&b, "  Lyrics: %s\n", strings.Join(lyrics, " / "))
		}
	}

	b.WriteString("\nRules:\n")
	b.WriteString("- Time every cut, camera move and action to the music at the listed position.\n")
	b.WriteString("- Show only the listed characters, one instance of each; never duplicate a character within a shot.\n")
	b.WriteString("- No text, captions, subtitles, lyrics, logos, or watermarks in frame.\n")
	return b.String()
}

func writeBriefLine(b *strings.Builder, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		fmt.Fprintf(b, "%s: %s\n", label, value)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// characterIDsInOrder は、カットに現れるキャラクター ID を初出順で返します。
func characterIDsInOrder(cuts []video.Cut) []string {
	var ids []string
	seen := map[string]bool{}
	for _, cut := range cuts {
		id := strings.TrimSpace(cut.CharacterID)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func characterName(id string, characters *characterkit.Characters) string {
	if characters != nil {
		if char := characters.GetCharacter(id); char != nil && strings.TrimSpace(char.Name) != "" {
			return strings.TrimSpace(char.Name)
		}
	}
	return id
}

func sectionName(recipe *video.Recipe, sectionIndex int) string {
	sections := recipe.MusicRecipe.Sections
	if sectionIndex < 1 || sectionIndex > len(sections) {
		return ""
	}
	return strings.TrimSpace(sections[sectionIndex-1].Name)
}

func lyricLines(dialogue string) []string {
	var lines []string
	for line := range strings.SplitSeq(dialogue, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// foldSharedDescription は、id のカットに共通する外見描写を anchors の中で name へ畳み、
// その描写を返します。まとめるほどの共通部分が無ければ何も変えず空文字を返します。
//
// 描写はカタログの visual_cues とは一致しません（台本生成の LLM が言い換えるうえ、
// カタログも後から変わる）。そのためカタログとは照合せず、カット同士の共通部分を
// 取ります。共通部分を持たないカット（キャラクターが映らない情景など）は畳む対象から
// 外すだけで、全体の畳み込みは止めません。
func foldSharedDescription(cuts []video.Cut, anchors []string, id, name string) string {
	var shared []rune
	var members []int
	for i, cut := range cuts {
		if strings.TrimSpace(cut.CharacterID) != id || anchors[i] == "" {
			continue
		}
		if shared == nil {
			shared = []rune(anchors[i])
			members = append(members, i)
			continue
		}
		if common := longestCommonRunes(shared, []rune(anchors[i])); len(common) >= sharedDescriptionMinRunes {
			shared = common
			members = append(members, i)
		}
	}
	if len(members) < 2 {
		return ""
	}

	block := string(shared)
	// 名前から始める。共通部分は "close-up of Tsumugi, a girl ..." の " of " のように、
	// 描写の手前の語まで含みうるので、そこを置き換えると文が壊れます。
	if at := strings.Index(block, name); at >= 0 {
		block = block[at:]
	}
	// 描写の終わりで切る。共通部分は次の語の頭文字まで伸びていることがあります
	// （"..., sprinting" と "..., singing" なら "..., s" まで一致する）。
	if end := strings.LastIndexAny(block, ",."); end > 0 {
		block = block[:end]
	}
	if len([]rune(block)) < sharedDescriptionMinRunes {
		return ""
	}
	for _, i := range members {
		anchors[i] = strings.Replace(anchors[i], block, name, 1)
	}
	return strings.TrimLeft(strings.TrimPrefix(block, name), ", ")
}

// longestCommonRunes は a と b に共通する最長の連続部分を返します。
func longestCommonRunes(a, b []rune) []rune {
	best, bestEnd := 0, 0
	prev := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best, bestEnd = cur[j], i
				}
			}
		}
		prev = cur
	}
	return a[bestEnd-best : bestEnd]
}

// formatTimestamp は秒を m:ss にします（端数は切り捨て）。
func formatTimestamp(sec float64) string {
	total := int(sec)
	if total < 0 {
		total = 0
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
