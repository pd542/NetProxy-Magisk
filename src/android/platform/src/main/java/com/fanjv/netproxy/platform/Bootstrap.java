package com.fanjv.netproxy.platform;

import dalvik.system.InMemoryDexClassLoader;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.ByteBuffer;

/**
 * app_process 的入口引导。
 *
 * <p>厂商（vivo 等）在 DexFile.openDexFile 中植入了动态加载检测，用
 * -Djava.class.path 指定外部 dex 时会在 JNI 就绪前抛
 * UnsatisfiedLinkError 并终止进程。这里改为从 stdin 读取 dex 字节，用
 * InMemoryDexClassLoader 从内存加载，不经过文件式 dex 加载路径。
 *
 * <p>输入帧：&lt;dex 长度:u32 大端&gt;&lt;dex 字节&gt;&lt;原样转交给 Main 的请求 JSON&gt;。
 */
public final class Bootstrap {
    private static final int DEX_LIMIT = 32 << 20;

    private Bootstrap() {}

    public static void main(String[] args) throws Exception {
        byte[] dex = readDex();
        ClassLoader loader = new InMemoryDexClassLoader(ByteBuffer.wrap(dex), Bootstrap.class.getClassLoader());
        Class<?> entry = Class.forName("com.fanjv.netproxy.platform.Main", true, loader);
        entry.getMethod("main", String[].class).invoke(null, (Object) new String[0]);
    }

    /** 读出带长度前缀的 dex；余下 stdin 保持不动，由 Main 继续读取请求。 */
    private static byte[] readDex() throws Exception {
        InputStream input = System.in;
        byte[] header = new byte[4];
        if (!fill(input, header)) throw new IllegalStateException();
        long size = ((long) (header[0] & 0xff) << 24) | ((header[1] & 0xff) << 16)
            | ((header[2] & 0xff) << 8) | (header[3] & 0xff);
        if (size <= 0 || size > DEX_LIMIT) throw new IllegalStateException();
        byte[] dex = new byte[(int) size];
        if (!fill(input, dex)) throw new IllegalStateException();
        return dex;
    }

    private static boolean fill(InputStream input, byte[] target) throws Exception {
        int offset = 0;
        while (offset < target.length) {
            int read = input.read(target, offset, target.length - offset);
            if (read < 0) return false;
            offset += read;
        }
        return true;
    }
}
