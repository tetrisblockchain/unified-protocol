import os

structure = {
    # Vercel Configuration
    "unified-protocol/vercel.json": """{
  "version": 2,
  "cleanUrls": true,
  "framework": null
}""",

    # The Home Website (Landing Page)
    "unified-protocol/index.html": """
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8"><title>UniFied | The Web Ledger</title>
    <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-black text-white font-sans">
    <nav class="p-6 flex justify-between max-w-7xl mx-auto">
        <div class="text-2xl font-bold">UFI<span class="text-blue-500">NI</span>FIED</div>
        <div class="space-x-6"><a href="/search" class="hover:text-blue-400">Search</a></div>
    </nav>
    <header class="py-24 text-center">
        <h1 class="text-7xl font-extrabold mb-6">The Web, <span class="text-blue-400">Decentralized.</span></h1>
        <p class="text-gray-400 mb-10">UniFied is a Layer 1 blockchain indexing the global internet via PoUW.</p>
        <div class="flex justify-center space-x-4">
            <a href="/search" class="bg-blue-600 px-8 py-4 rounded-lg font-bold">Try Search</a>
            <button class="border border-gray-700 px-8 py-4 rounded-lg">Read Docs</button>
        </div>
    </header>
</body>
</html>""",

    # The Search Interface
    "unified-protocol/search.html": """
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8"><title>UniFied Search</title>
    <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-[#0b0e14] text-white">
    <div class="max-w-4xl mx-auto pt-20 px-6">
        <h2 class="text-3xl font-bold mb-8 text-center">Search the <span class="text-blue-500">Ledger</span></h2>
        <div class="relative">
            <input type="text" placeholder="Keywords or UFI address..." class="w-full bg-gray-900 border border-gray-700 p-4 rounded-xl focus:border-blue-500 outline-none">
        </div>
        <div id="results" class="mt-10 space-y-6">
            <div class="p-6 border border-gray-800 rounded-xl bg-gray-900/50">
                <p class="text-blue-400 font-bold">Genesis Crawl Data</p>
                <p class="text-sm text-gray-500">Block #1 - Miner: UFI_Architect</p>
            </div>
        </div>
    </div>
</body>
</html>""",
}

def build_web():
    print("🌐 Building UniFied Vercel Deployment...")
    for path, content in structure.items():
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(content.strip())
    print("\n✅ Vercel-ready files created in 'unified-protocol/'")

if __name__ == "__main__":
    build_web()