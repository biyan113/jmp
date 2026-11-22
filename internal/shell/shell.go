package shell

import (
	"fmt"
	"strings"
)

// InitScript returns the integration script for shell. jmpBin is the path of
// the jmp binary; it is quoted for the target shell, so paths containing
// spaces or quotes are safe.
func InitScript(shell, jmpBin string) (string, error) {
	switch shell {
	case "bash":
		return render(bashScript, posixQuote(jmpBin)), nil
	case "zsh":
		return render(zshScript, posixQuote(jmpBin)), nil
	case "fish":
		return render(fishScript, fishQuote(jmpBin)), nil
	case "powershell", "pwsh":
		return render(powershellScript, powershellQuote(jmpBin)), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish, powershell)", shell)
	}
}

func render(tmpl, bin string) string {
	tmpl = strings.ReplaceAll(tmpl, "{{j}}", posixJ)
	return strings.ReplaceAll(tmpl, "{{bin}}", bin)
}

// posixQuote single-quotes s for bash/zsh.
func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote single-quotes s for fish (backslash escapes \ and ').
func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// powershellQuote single-quotes s for PowerShell (' is doubled).
func powershellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// posixJ is the j/jc function shared verbatim by bash and zsh.
const posixJ = `j() {
    local result _rc=0 _kw=1
    # "j -- back" treats the words literally instead of as navigation keywords.
    if [ "$1" = "--" ]; then shift; _kw=0; fi
    if [ $# -eq 0 ]; then
        result=$({{bin}} --tui 2>/dev/null)
    else
        if [ $_kw -eq 1 ] && [ $# -eq 1 ]; then
            local current="$PWD"
            case "$1" in
            -)
                cd "$OLDPWD"
                return
                ;;
            back)
                if [ -n "$JMP_BACK" ]; then
                    cd "$JMP_BACK" && JMP_FWD="$current"
                    return
                fi
                ;;
            fwd)
                if [ -n "$JMP_FWD" ]; then
                    cd "$JMP_FWD" && JMP_BACK="$current"
                    return
                fi
                ;;
            esac
        fi
        result=$({{bin}} jump --skip "$PWD" -- "$@" 2>/dev/null)
        _rc=$?
    fi
    if [ -n "$result" ] && [ -d "$result" ]; then
        JMP_BACK="$PWD"
        JMP_FWD=""
        cd "$result"
    elif [ $# -gt 0 ]; then
        # jmp DB miss: fall back to treating the args as a directory path
        # (supports ~ expansion, absolute paths, and relative names).
        local _dir
        if [ $# -eq 1 ]; then _dir="$1"; else _dir="$*"; fi
        _dir="${_dir/#\~/$HOME}"
        if [ -d "$_dir" ]; then
            JMP_BACK="$PWD"
            JMP_FWD=""
            cd "$_dir"
        elif [ $_rc -ne 0 ] && [ $_rc -ne 3 ]; then
            # A real failure (not just "no match"): re-run to show the error.
            {{bin}} jump --skip "$PWD" -- "$@" >/dev/null
            return 1
        else
            echo "jmp: no match for: $*" >&2
            {{bin}} suggest -- "$@" 2>/dev/null | sed 's/^/  /' >&2
            return 1
        fi
    fi
}

jc() { j "$@"; }
`

const bashScript = `# jmp shell integration - bash
# Add to ~/.bashrc: eval "$(jmp init bash)"

# Runs before every prompt, but only records when the directory changed, so
# ranking means the same thing as under zsh/fish (visits, not commands typed).
_jmp_chpwd() {
    [ "$PWD" = "$_JMP_LAST_PWD" ] && return
    _JMP_LAST_PWD="$PWD"
    {{bin}} add -- "$PWD" >/dev/null 2>&1
    # Returns immediately; a due sync runs in a detached process.
    {{bin}} sync auto >/dev/null 2>&1
}

{{j}}
_jmp_complete() {
    local cur="${COMP_WORDS[COMP_CWORD]}" c
    local IFS=$'\n'
    COMPREPLY=()
    for c in $({{bin}} complete -- "$cur" 2>/dev/null); do
        COMPREPLY+=("$(printf '%q' "$c")")
    done
}
complete -F _jmp_complete j jc

if [[ "$PROMPT_COMMAND" != *"_jmp_chpwd"* ]]; then
    PROMPT_COMMAND="_jmp_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi
`

const zshScript = `# jmp shell integration - zsh
# Add to ~/.zshrc: eval "$(jmp init zsh)"

_jmp_chpwd() {
    {{bin}} add -- "$PWD" >/dev/null 2>&1
    # Returns immediately; a due sync runs in a detached process.
    {{bin}} sync auto >/dev/null 2>&1
}

{{j}}
_jmp_complete() {
    local -a candidates
    candidates=("${(@f)$({{bin}} complete -- "$words[CURRENT]" 2>/dev/null)}")
    _describe 'jmp candidates' candidates
}
compdef _jmp_complete j jc

autoload -Uz add-zsh-hook
add-zsh-hook chpwd _jmp_chpwd
`

const fishScript = `# jmp shell integration - fish
# Add to ~/.config/fish/config.fish: jmp init fish | source

function _jmp_chpwd --on-variable PWD
    {{bin}} add -- (pwd) >/dev/null 2>&1
    # Returns immediately; a due sync runs in a detached process.
    {{bin}} sync auto >/dev/null 2>&1
end

function j
    set -l result
    set -l rc 0
    set -l kw 1
    # "j -- back" treats the words literally instead of as navigation keywords.
    if test (count $argv) -gt 0; and test "$argv[1]" = "--"
        set -e argv[1]
        set kw 0
    end
    if test (count $argv) -eq 0
        set result ({{bin}} --tui 2>/dev/null)
    else
        if test $kw -eq 1; and test (count $argv) -eq 1
            if test "$argv[1]" = "-"
                cd $dirprev[-1]
                return
            end
            if test "$argv[1]" = "back"; and test -n "$JMP_BACK"
                set -l current (pwd)
                cd "$JMP_BACK"
                set -gx JMP_FWD "$current"
                return
            end
            if test "$argv[1]" = "fwd"; and test -n "$JMP_FWD"
                set -l current (pwd)
                cd "$JMP_FWD"
                set -gx JMP_BACK "$current"
                return
            end
        end
        set result ({{bin}} jump --skip (pwd) -- $argv 2>/dev/null)
        set rc $status
    end
    if test -n "$result" -a -d "$result"
        set -gx JMP_BACK (pwd)
        set -gx JMP_FWD ""
        cd $result
    else if test (count $argv) -gt 0
        # jmp DB miss: fall back to treating args as a directory path.
        if test (count $argv) -eq 1
            set _dir (string replace -r '^~' $HOME -- $argv[1])
        else
            set _dir (string replace -r '^~' $HOME -- "$argv")
        end
        if test -d "$_dir"
            set -gx JMP_BACK (pwd)
            set -gx JMP_FWD ""
            cd $_dir
        else if test $rc -ne 0 -a $rc -ne 3
            # A real failure (not just "no match"): re-run to show the error.
            {{bin}} jump --skip (pwd) -- $argv >/dev/null
            return 1
        else
            echo "jmp: no match for: $argv" >&2
            {{bin}} suggest -- $argv 2>/dev/null | sed 's/^/  /' >&2
            return 1
        end
    end
end

function jc
    j $argv
end

complete -c j -f -a '({{bin}} complete -- (commandline -ct) 2>/dev/null)'
complete -c jc -f -a '({{bin}} complete -- (commandline -ct) 2>/dev/null)'
`

const powershellScript = `# jmp shell integration - PowerShell
# Add to $PROFILE: Invoke-Expression (& jmp init powershell | Out-String)

$global:_jmpLastDir = $null
$global:_jmpLastPwd = $null

function global:j {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Query)
    $result = $null
    $rc = 0
    if ($Query.Count -eq 0) {
        $result = & {{bin}} --tui 2>$null
    } elseif ($Query.Count -eq 1 -and $Query[0] -eq '-') {
        $prev = $global:_jmpLastDir
        if ($prev) {
            $global:_jmpLastDir = (Get-Location).Path
            Set-Location $prev
        }
        return
    } elseif ($Query.Count -eq 1 -and $Query[0] -eq 'back' -and $global:_jmpLastDir) {
        $prev = $global:_jmpLastDir
        $global:_jmpLastDir = (Get-Location).Path
        Set-Location $prev
        return
    } else {
        $result = & {{bin}} jump --skip (Get-Location).Path -- @Query 2>$null
        $rc = $LASTEXITCODE
    }
    if ($result -and (Test-Path $result -PathType Container)) {
        $global:_jmpLastDir = (Get-Location).Path
        Set-Location $result
    } elseif ($Query.Count -gt 0) {
        # jmp DB miss: fall back to treating args as a directory path.
        $dir = ($Query -join ' ') -replace '^~', $HOME
        if (Test-Path $dir -PathType Container) {
            $global:_jmpLastDir = (Get-Location).Path
            Set-Location $dir
        } elseif ($rc -ne 0 -and $rc -ne 3) {
            # A real failure (not just "no match"): re-run to show the error.
            & {{bin}} jump --skip (Get-Location).Path -- @Query | Out-Null
        } else {
            Write-Error "jmp: no match for: $($Query -join ' ')"
            & {{bin}} suggest -- @Query 2>$null | ForEach-Object { Write-Error "  $_" }
        }
    }
}

$global:_jmpOriginalPrompt = $function:prompt
function global:prompt {
    # Record only when the directory changed (visits, not commands typed).
    $here = (Get-Location).Path
    if ($here -ne $global:_jmpLastPwd) {
        $global:_jmpLastPwd = $here
        & {{bin}} add -- $here 2>$null | Out-Null
        # Returns immediately; a due sync runs in a detached process.
        & {{bin}} sync auto 2>$null | Out-Null
    }
    if ($global:_jmpOriginalPrompt) { & $global:_jmpOriginalPrompt }
}
`
