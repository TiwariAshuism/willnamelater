import {
  TrendingUp,
  Zap,
  LayoutGrid,
  Brush,
  Building2,
  Heart,
  Gem,
  ShoppingBag,
  CreditCard,
  Truck,
  BarChart3,
  Upload,
  Eye,
  Mail,
  MapPin,
  type LucideIcon,
} from "lucide-react";

const REGISTRY: Record<string, LucideIcon> = {
  TrendingUp,
  Zap,
  LayoutGrid,
  Brush,
  Building2,
  Heart,
  Gem,
  ShoppingBag,
  CreditCard,
  Truck,
  BarChart3,
  Upload,
  Eye,
  Mail,
  MapPin,
};

export function Icon({
  name,
  size = 18,
  color,
  className,
}: {
  name: string;
  size?: number;
  color?: string;
  className?: string;
}) {
  const Cmp = REGISTRY[name] ?? LayoutGrid;
  return <Cmp size={size} color={color} strokeWidth={2} className={className} />;
}
