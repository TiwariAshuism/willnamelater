import { Background } from "@/components/layout/background";
import { PageLoader } from "@/components/layout/page-loader";
import { Navbar } from "@/components/layout/navbar";
import { Footer } from "@/components/layout/footer";
import { Hero } from "@/components/sections/hero";
import { Ecommerce } from "@/components/sections/ecommerce";
import { HowItWorks } from "@/components/sections/how-it-works";
import { ClassCarousel } from "@/components/sections/class-carousel";
import { LogoStrip } from "@/components/sections/logo-strip";
import { Stats } from "@/components/sections/stats";
import { StoryHeadline } from "@/components/sections/story-headline";
import { ArtMeetsMarket } from "@/components/sections/art-meets-market";
import { Vision } from "@/components/sections/vision";
import { CommunityMarquee } from "@/components/sections/community-marquee";
import { StoryBento } from "@/components/sections/story-bento";
import { Marketplace } from "@/components/sections/marketplace";
import { IntegrationsDock } from "@/components/sections/integrations-dock";
import { Spotlight } from "@/components/sections/spotlight";
import { Membership } from "@/components/sections/membership";
import { YellowBand } from "@/components/sections/yellow-band";
import { FeatureCards } from "@/components/sections/feature-cards";

export default function HomePage() {
  return (
    <div className="relative w-full overflow-x-clip bg-canvas">
      <PageLoader />
      <Background />
      <Navbar />
      <main>
        <Hero />
        <Ecommerce />
        <HowItWorks />
        <ClassCarousel />
        <LogoStrip />
        <Stats />
        <StoryHeadline />
        <ArtMeetsMarket />
        <Vision />
        <CommunityMarquee />
        <StoryBento />
        <Marketplace />
        <IntegrationsDock />
        <Spotlight />
        <Membership />
        <YellowBand />
        <FeatureCards />
      </main>
      <Footer />
    </div>
  );
}
