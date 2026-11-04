# zpaq-go

[![Go Reference](https://pkg.go.dev/badge/github.com/morsuning/zpaq-go.svg)](https://pkg.go.dev/github.com/morsuning/zpaq-go)
[![CI](https://github.com/morsuning/zpaq-go/actions/workflows/ci.yml/badge.svg)](https://github.com/morsuning/zpaq-go/actions/workflows/ci.yml)

[ZPAQ](https://mattmahoney.net/dc/zpaq.html) 压缩格式的纯 Go 实现。不依赖 cgo 和任何第三方库，要求 Go 1.21+。
它生成的压缩文件可以被官方 `zpaq` 程序解压，也能读取 `zpaq` 生成的文件。

[English](README.md)

```
go get github.com/morsuning/zpaq-go
```

```go
archive, err := zpaq.Compress(nil, data, zpaq.WithLevel(zpaq.Best), zpaq.WithName("data.bin"))
if err != nil { ... }
restored, err := zpaq.Decompress(nil, archive)
```

流式接口（多个块在多核上并行压缩）：

```go
w := zpaq.NewWriter(dst, zpaq.WithLevel(zpaq.Default), zpaq.WithName("log.txt"))
io.Copy(w, src)
w.Close()

r := zpaq.NewReader(archive)
defer r.Close()
io.Copy(out, r)
```

## 为什么用 ZPAQ

ZPAQ 是上下文混合（context mixing）格式：每个块都自带模型，所以任何解码器都能读任何编码器的输出，
压缩率与 PAQ 同级。在下面的样本上，5 级输出比 `xz -9e` 小约 24%。

## 兼容性

本库生成的是 ZPAQ 流式格式（streaming）压缩文件，官方程序可以直接解压：

```
$ zpaq x archive.zpaq
> data.bin
1 streaming segments extracted
0.027 seconds (all OK)
```

测试套件（`interop_test.go`，在 CI 中运行）用 **zpaq 7.15** 做了双向验证：

- zpaq 能逐字节还原本库 0–5 级生成的压缩文件，包括单块/多块、有/无文件名、多个文件拼接，以及流式 `Writer` 的输出；
- 本库能解码 zpaq 用 `-method 0` 到 `5`（含 LZ77、BWT 后处理程序）和若干高级方法生成的归档，并逐段校验 SHA-1；
  对没有重复片段的输入，数据块内容与原文一致。

限制：

- zpaq 默认的“日志式”归档（`zpaq a`）用索引块和数据块记录文件、版本和去重片段。`Walk` 能解码其中的各个块，
  但本库不解释这套日志，要列出或提取其中的文件请用 `zpaq`。
- 不支持加密（`zpaq -key`）。
- 4、5 级生成与 `zpaq -method 4/5` 相同的上下文混合模型，但不使用 zpaq 的 LZ77、BWT、E8E9 预处理，
  所以对 zpaq 会选用这些预处理的数据，压缩后大小可能与 zpaq 不同。

## 压缩级别

| 级别 | 模型 | 3 MB 文本样本 |
|---|---|---|
| 0 `Store` | 无 | 3,000,259 |
| 1 `Fast` | libzpaq "min" | 415,926 |
| 2 | libzpaq "mid" | 210,497 |
| 3 | libzpaq "max" | 194,285 |
| 4 `Default` | 类似 `zpaq -m4` | 208,336 |
| 5 `Best` | 类似 `zpaq -m5` | 196,663 |

样本是一个文档 tar 包的前 3,000,000 字节（以文本为主）。同样的数据：`xz -9e` 为 258,988；
`zpaq -method 4` 为 210,128，`zpaq -method 5` 为 198,408（含日志索引开销）。
最佳级别取决于数据，需要最小体积时可多试几个级别。

速度是上下文混合的典型水平：高级别每核每秒几百 KB（5 级约 0.2 MB/s，耗时约为 C 参考实现的 2 倍），
解压与压缩一样慢。输入默认按 16 MiB 分块（`WithBlockSize`），块在最多 4 个 goroutine 上并行
（`WithConcurrency`），输出与并发数无关。

## 处理不可信输入

解码器会运行压缩文件里的 ZPAQL 程序，因此做了限制：指令步数预算、模型内存上限 `WithMaxMemory`
（默认每块 4 GiB）、输出大小上限 `WithMaxOutput`、严格的容器检查，以及用 `WithContext` 取消。
格式错误会返回包装了 `zpaq.ErrCorrupt` 的错误，不会 panic（fuzz 测试覆盖）。
解码不可信数据时请设置这两个上限。

## 命令行

```
go install github.com/morsuning/zpaq-go/cmd/zpaq-go@latest

zpaq-go c -level 5 -o notes.zpaq notes.txt     # 压缩
zpaq-go d -o notes.txt notes.zpaq              # 解压
zpaq-go l notes.zpaq                           # 列出段
zpaq-go t notes.zpaq                           # 校验
```

## ctxmix

`github.com/morsuning/zpaq-go/ctxmix` 是另一个独立编解码器，格式自成一体（**不**兼容 ZPAQ）：
更强的上下文混合模型，在上面的样本上得到 189,008 字节，比 ZPAQ 5 级小 3.9%，比 xz 小 27%。
只有你自己的代码读取数据时可以用它。

## 致谢与许可

格式和参考实现来自 Matt Mahoney，属于公有领域，详见 [NOTICE.md](NOTICE.md)。本库采用 MIT 许可证。
