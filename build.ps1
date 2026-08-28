$ErrorActionPreference = "Stop"

# build.ps1 — gera o abraham.exe pronto para distribuir.
#
# Nao instala nada e nao baixa nada alem das dependencias ja fixadas no go.mod.
# Quem for recompilar isto daqui a anos precisa so do Go.
#
# Os portoes rodam ANTES de compilar. Um executavel gerado a partir de codigo
# que nao passou no teste e pior do que nenhum executavel: ele parece pronto.

$raiz = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $raiz

# Quem acabou de instalar o Go e nao reabriu o terminal ainda nao tem ele no
# PATH desta sessao. Procurar no lugar padrao evita um erro que parece grave e
# nao e.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $padrao = Join-Path $env:ProgramFiles "Go\bin"
    if (Test-Path (Join-Path $padrao "go.exe")) {
        $env:Path = "$padrao;$env:Path"
    } else {
        throw "Go nao encontrado. Instale de https://go.dev/dl/ e reabra o terminal."
    }
}

Write-Host "Conferindo o codigo..." -ForegroundColor DarkYellow

go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet reclamou" }

$malFormatados = gofmt -l .
if ($malFormatados) { throw "gofmt: $malFormatados" }

go test ./...
if ($LASTEXITCODE -ne 0) { throw "os testes falharam" }

Write-Host "Compilando..." -ForegroundColor DarkYellow

$dist = Join-Path $raiz "dist"
New-Item -ItemType Directory -Force $dist | Out-Null
$saida = Join-Path $dist "abraham.exe"

# -s -w tiram a tabela de simbolos: o executavel cai varios MB e ninguem depura
# isto em producao. -H=windowsgui NAO e usado de proposito: o Abraham E uma
# janela de console.
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o $saida ./cmd/abraham
if ($LASTEXITCODE -ne 0) { throw "a compilacao falhou" }

foreach ($arquivo in @("instalar.bat", "desinstalar.bat", "README.md")) {
    $origem = Join-Path $raiz $arquivo
    if (Test-Path $origem) { Copy-Item $origem $dist -Force }
}

$mb = [math]::Round((Get-Item $saida).Length / 1MB, 1)
Write-Host ""
Write-Host "Pronto: dist\abraham.exe ($mb MB)" -ForegroundColor Green
Write-Host "Copie a pasta dist para a maquina de destino. Dois cliques em abraham.exe"
Write-Host "ja abre; instalar.bat cria o atalho na Area de Trabalho e no Menu Iniciar."
