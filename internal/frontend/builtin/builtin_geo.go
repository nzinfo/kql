// Package builtin — Round 6: geo_* geospatial family (53 functions).
//
// All geo_* functions are NeedsPostProc (no portable SQL form; pg has PostGIS
// extension but it is optional). Registering them closes the catalog gap vs
// kqlparser so queries parse + translate without KQL003 unknown-function
// warnings. Real execution requires a geospatial backend.
package builtin

func init() {
	adds := []Spec{
		// H3 cell hierarchy
		{Name: "geo_h3cell_children", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_h3cell_level", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_h3cell_neighbors", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_h3cell_parent", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_h3cell_rings", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_h3cell_to_central_point", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_h3cell_to_polygon", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		// Geohash
		{Name: "geo_geohash_neighbors", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_geohash_to_central_point", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_geohash_to_polygon", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		// S2 cell
		{Name: "geo_s2cell_neighbors", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_s2cell_to_central_point", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_s2cell_to_polygon", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		// Point conversions
		{Name: "geo_point_to_geohash", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "geo_point_to_h3cell", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "geo_point_to_s2cell", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		// Distance / geometry
		{Name: "geo_distance_2points", MinArgs: 4, MaxArgs: 4, NeedsPostProc: true},
		{Name: "geo_distance_point_to_line", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_distance_point_to_polygon", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_angle", MinArgs: 4, MaxArgs: 4, NeedsPostProc: true},
		{Name: "geo_azimuth", MinArgs: 4, MaxArgs: 4, NeedsPostProc: true},
		{Name: "geo_closest_point_on_line", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_closest_point_on_polygon", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Line
		{Name: "geo_line_buffer", MinArgs: 2, MaxArgs: 4, NeedsPostProc: true},
		{Name: "geo_line_centroid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_line_densify", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_line_interpolate_point", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_line_length", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_line_locate_point", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_line_simplify", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_line_to_s2cells", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		// Polygon
		{Name: "geo_polygon_area", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_polygon_buffer", MinArgs: 2, MaxArgs: 4, NeedsPostProc: true},
		{Name: "geo_polygon_centroid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_polygon_densify", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_polygon_perimeter", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_polygon_simplify", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_polygon_to_h3cells", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "geo_polygon_to_s2cells", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		// Point in
		{Name: "geo_point_buffer", MinArgs: 3, MaxArgs: 5, NeedsPostProc: true},
		{Name: "geo_point_in_circle", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "geo_point_in_polygon", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Intersections
		{Name: "geo_intersection_2lines", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_intersection_2polygons", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_intersection_line_with_polygon", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_intersects_2lines", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_intersects_2polygons", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_intersects_line_with_polygon", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Simplify / union arrays
		{Name: "geo_simplify_polygons_array", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "geo_union_lines_array", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_union_polygons_array", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		// Conversions
		{Name: "geo_from_wkt", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "geo_info_from_ip_address", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
	}
	for _, s := range adds {
		catalog[normalize(s.Name)] = s
	}
}
