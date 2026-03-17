import SwiftUI

struct WalletView: View {
    @State private var balance: Double = 333.33
    @State private var ufiAddress: String = "UFI_Architect_7k9w...4rQz"
    @State private var username: String = "Architect"
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.edgesIgnoringSafeArea(.all)
                
                VStack(spacing: 25) {
                    // --- Balance Card ---
                    VStack {
                        Text("Total Balance")
                            .font(.caption)
                            .foregroundColor(.gray)
                        Text("\(String(format: "%.2f", balance)) UFD")
                            .font(.system(size: 48, weight: .bold, design: .monospaced))
                            .foregroundColor(.cyan)
                    }
                    .padding(.top, 40)
                    
                    // --- Identity Chip ---
                    HStack {
                        Image(systemName: "person.crop.circle.fill")
                        Text(username)
                            .fontWeight(.bold)
                        Spacer()
                        Text("Verified")
                            .font(.caption2)
                            .padding(5)
                            .background(Color.blue.opacity(0.3))
                            .cornerRadius(5)
                    }
                    .padding()
                    .background(Color.white.opacity(0.05))
                    .cornerRadius(15)
                    
                    // --- Action Buttons ---
                    HStack(spacing: 20) {
                        ActionButton(icon: "arrow.up.right", label: "Send")
                        ActionButton(icon: "arrow.down.left", label: "Receive")
                        ActionButton(icon: "magnifyingglass", label: "Tasks")
                    }
                    
                    // --- Architect Revenue Feed ---
                    VStack(alignment: .leading) {
                        Text("ARCHITECT REVENUE (3.33%)")
                            .font(.caption2)
                            .tracking(2)
                            .foregroundColor(.gray)
                            .padding(.bottom, 10)
                        
                        RevenueRow(amount: "+0.45", source: "Crawl Task #1042")
                        RevenueRow(amount: "+1.20", source: "UNS Registration: 'Founder'")
                        RevenueRow(amount: "+0.08", source: "Search Bounty: 'AI Trends'")
                    }
                    .padding()
                    .background(Color.white.opacity(0.03))
                    .cornerRadius(20)
                    
                    Spacer()
                }
                .padding()
            }
            .navigationTitle("UniFied Wallet")
            .toolbarColorScheme(.dark, for: .navigationBar)
        }
    }
}

// Custom Components
struct ActionButton: View {
    var icon: String
    var label: String
    var body: some View {
        VStack {
            Image(systemName: icon)
                .font(.title2)
                .frame(width: 60, height: 60)
                .background(Color.blue)
                .clipShape(Circle())
            Text(label).font(.caption).foregroundColor(.white)
        }
    }
}

struct RevenueRow: View {
    var amount: String
    var source: String
    var body: some View {
        HStack {
            Text(source).font(.subheadline)
            Spacer()
            Text(amount).foregroundColor(.green).font(.system(.subheadline, design: .monospaced))
        }
        .padding(.vertical, 5)
    }
}
