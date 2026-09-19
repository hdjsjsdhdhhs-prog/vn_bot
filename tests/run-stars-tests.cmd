@echo off
setlocal
set "CGO_ENABLED=1"
set "CC=C:\msys64\ucrt64\bin\gcc.exe"
set "PATH=C:\msys64\ucrt64\bin;%PATH%"
go test -p 1 %*
exit /b %errorlevel%
