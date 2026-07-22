package shell

import "fmt"

func InitScript(shell, jmpBin string) (string, error) {
	switch shell {
	case "bash":
		return bashScript(jmpBin), nil
	case "zsh":
		return zshScript(jmpBin), nil
	case "fish":
		return fishScript(jmpBin), nil
	case "powershell", "pwsh":
		return powershellScript(jmpBin), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish, powershell)", shell)
	}
}

func bashScript(bin string) string {
	return fmt.Sprintf(`# jmp shell integration - bash
# Add to ~/.bashrc: eval "$(jmp init bash)"

_jmp_chpwd() {
    %s add "$(pwd)" 2>/dev/null
    (%s sync auto &>/dev/null &)
}

j() {
    local result
    if [ $# -eq 0 ]; then
        result=$(%s --tui 2>/dev/null)
    else
        if [ "$1" = "-" ]; then
            cd "$OLDPWD"
            return
        fi
        if [ "$1" = "back" ] && [ -n "$JMP_BACK" ]; then
            local current="$(pwd)"
            cd "$JMP_BACK"
            JMP_FWD="$current"
            return
        fi
        if [ "$1" = "fwd" ] && [ -n "$JMP_FWD" ]; then
            local current="$(pwd)"
            cd "$JMP_FWD"
            JMP_BACK="$current"
            return
        fi
        result=$(%s jump --skip "$(pwd)" -- "$@" 2>/dev/null)
    fi
    if [ -n "$result" ] && [ -d "$result" ]; then
        JMP_BACK="$(pwd)"
        JMP_FWD=""
        cd "$result"
    elif [ $# -gt 0 ]; then
        # jmp DB miss: fall back to treating the args as a directory path
        # (supports ~ expansion, absolute paths, and relative names).
        local _dir
        if [ $# -eq 1 ]; then _dir="$1"; else _dir="$*"; fi
        _dir="${_dir/#\~/$HOME}"
        if [ -d "$_dir" ]; then
            JMP_BACK="$(pwd)"
            JMP_FWD=""
            cd "$_dir"
        else
            echo "jmp: no match for: $*" >&2
            %s suggest "$@" 2>/dev/null | sed 's/^/  /' >&2
            return 1
        fi
    fi
}

jc() { j "$@"; }

_jmp_complete() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    COMPREPLY=( $(compgen -W "$(%s complete "$cur" 2>/dev/null)" -- "$cur") )
}
complete -F _jmp_complete j jc

if [[ "$PROMPT_COMMAND" != *"_jmp_chpwd"* ]]; then
    PROMPT_COMMAND="_jmp_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi
`, bin, bin, bin, bin, bin, bin)
}

func zshScript(bin string) string {
	return fmt.Sprintf(`# jmp shell integration - zsh
# Add to ~/.zshrc: eval "$(jmp init zsh)"

_jmp_chpwd() {
    %s add "$(pwd)" 2>/dev/null
    (%s sync auto &>/dev/null &)
}

j() {
    local result
    if [ $# -eq 0 ]; then
        result=$(%s --tui 2>/dev/null)
    else
        if [ "$1" = "-" ]; then
            cd "$OLDPWD"
            return
        fi
        if [ "$1" = "back" ] && [ -n "$JMP_BACK" ]; then
            local current="$(pwd)"
            cd "$JMP_BACK"
            JMP_FWD="$current"
            return
        fi
        if [ "$1" = "fwd" ] && [ -n "$JMP_FWD" ]; then
            local current="$(pwd)"
            cd "$JMP_FWD"
            JMP_BACK="$current"
            return
        fi
        result=$(%s jump --skip "$(pwd)" -- "$@" 2>/dev/null)
    fi
    if [ -n "$result" ] && [ -d "$result" ]; then
        JMP_BACK="$(pwd)"
        JMP_FWD=""
        cd "$result"
    elif [ $# -gt 0 ]; then
        # jmp DB miss: fall back to treating the args as a directory path
        # (supports ~ expansion, absolute paths, and relative names).
        local _dir
        if [ $# -eq 1 ]; then _dir="$1"; else _dir="$*"; fi
        _dir="${_dir/#\~/$HOME}"
        if [ -d "$_dir" ]; then
            JMP_BACK="$(pwd)"
            JMP_FWD=""
            cd "$_dir"
        else
            echo "jmp: no match for: $*" >&2
            %s suggest "$@" 2>/dev/null | sed 's/^/  /' >&2
            return 1
        fi
    fi
}

jc() { j "$@"; }

_jmp_complete() {
    local -a candidates
    candidates=("${(@f)$('%s' complete "$words[CURRENT]" 2>/dev/null)}")
    _describe 'jmp candidates' candidates
}
compdef _jmp_complete j jc

autoload -Uz add-zsh-hook
add-zsh-hook chpwd _jmp_chpwd
`, bin, bin, bin, bin, bin, bin)
}

func fishScript(bin string) string {
	return fmt.Sprintf(`# jmp shell integration - fish
# Add to ~/.config/fish/config.fish: jmp init fish | source

function _jmp_chpwd --on-variable PWD
    %s add (pwd) 2>/dev/null
    %s sync auto &>/dev/null &
end

function j
    set -l result
    if test (count $argv) -eq 0
        set result (%s --tui 2>/dev/null)
    else
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
        set result (%s jump --skip (pwd) -- $argv 2>/dev/null)
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
        else
            echo "jmp: no match for: $argv" >&2
            %s suggest $argv 2>/dev/null | sed 's/^/  /' >&2
            return 1
        end
    end
end

complete -c j -f -a '(%s complete (commandline -ct) 2>/dev/null)'
complete -c jc -f -a '(%s complete (commandline -ct) 2>/dev/null)'
`, bin, bin, bin, bin, bin, bin, bin)
}

func powershellScript(bin string) string {
	return fmt.Sprintf(`# jmp shell integration - PowerShell
# Add to $PROFILE: Invoke-Expression (& jmp init powershell | Out-String)

$global:_jmpLastDir = $null

function global:j {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Query)
    $result = $null
    if ($Query.Count -eq 0) {
        $result = & '%s' --tui 2>$null
    } elseif ($Query[0] -eq '-') {
        $prev = $global:_jmpLastDir
        if ($prev) {
            $global:_jmpLastDir = (Get-Location).Path
            Set-Location $prev
        }
        return
    } elseif ($Query[0] -eq 'back' -and $global:_jmpLastDir) {
        $prev = $global:_jmpLastDir
        $global:_jmpLastDir = (Get-Location).Path
        Set-Location $prev
        return
    } else {
        $result = & '%s' jump --skip (Get-Location).Path -- @Query 2>$null
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
        } else {
            Write-Error "jmp: no match for: $($Query -join ' ')"
            & '%s' suggest @Query 2>$null | ForEach-Object { Write-Error "  $_" }
        }
    }
}

$global:_jmpOriginalPrompt = $function:prompt
function global:prompt {
    & '%s' add (Get-Location).Path 2>$null | Out-Null
    & '%s' sync auto 2>$null | Out-Null
    if ($global:_jmpOriginalPrompt) { & $global:_jmpOriginalPrompt }
}
`, bin, bin, bin, bin, bin)
}
