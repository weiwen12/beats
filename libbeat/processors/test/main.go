package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	//var Order = make(map[string]interface{})
	//
	//Order["order_id"] = "20190707212318"
	//
	//Order["order_price"] = 21.3
	//
	//Goods := make([]map[string]interface{}, 2)
	//
	//Goods[0] = make(map[string]interface{})
	//Goods[0]["goods_name"] = "手机"
	//Goods[0]["goods_price"] = 23.1
	//
	//Goods[1] = make(map[string]interface{})
	//Goods[1]["goods_name"] = "电脑"
	//Goods[1]["goods_price"] = 123.1
	//
	//GoodsColor := make([]map[string]interface{}, 2)
	//
	//GoodsColor[0] = make(map[string]interface{})
	//GoodsColor[0]["good_color"] = "红色"
	//
	//GoodsColor[1] = make(map[string]interface{})
	//GoodsColor[1]["good_color"] = "蓝色"
	//
	//Goods[0]["goods_color"] = GoodsColor
	//Goods[1]["goods_color"] = GoodsColor
	//
	//Order["good"] = Goods
	//
	//data, _ := json.Marshal(Order)
	//
	//fmt.Println(string(data))

	/*
		{
			"good": [{
				"goods_color": [{
					"good_color": "红色"
				}, {
					"good_color": "蓝色"
				}],
				"goods_name": "手机",
				"goods_price": 23.1
			}, {
				"goods_color": [{
					"good_color": "红色"
				}, {
					"good_color": "蓝色"
				}],
				"goods_name": "电脑",
				"goods_price": 123.1
			}],
			"order_id": "20190707212318",
			"order_price": 21.3
		}
	*/

	//var temp = make(map[string]interface{})
	data := '{"good":[{"goods_color":[{"good_color":"红色"},{"good_color":"蓝色"}],"goods_name":"手机","goods_price":23.1},{"goods_color":[{"good_color":"红色"},{"good_color":"蓝色"}],"goods_name":"电脑","goods_price":123.1}],"order_id":"20190707212318","order_price":21.3}'
	var temp interface{}

	err := json.Unmarshal(data, &temp)

	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(temp)
	fmt.Println("=========")
	//for s, i := range temp {
	//	fmt.Println(s, ":", i)
	//}
}
